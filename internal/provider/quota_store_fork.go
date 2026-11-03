package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/steady"
)

// QuotaStore is dormant until explicitly initialized or used. It has no default
// location, configuration lookup, repair path, or production owner yet.
type QuotaStore struct {
	path  string
	write func(string, []byte, fs.FileMode) error
}

type quotaFile struct {
	Version        uint64        `json:"version"`
	Epoch          string        `json:"epoch"`
	CommitSequence uint64        `json:"commit_sequence"`
	Records        []quotaRecord `json:"records"`
}

func NewQuotaStore(path string) *QuotaStore { return &QuotaStore{path: path} }

// Initialize creates only absent state; corrupt or unsupported bytes stay put.
func (s *QuotaStore) Initialize(ctx context.Context) (string, error) {
	target, release, err := s.lock(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	data, err := readQuotaFile(target)
	if err == nil {
		return data.Epoch, nil
	}
	if !errors.Is(err, ErrQuotaMissing) {
		return "", err
	}
	var epoch [16]byte
	if _, err := rand.Read(epoch[:]); err != nil {
		return "", quotaIO("epoch", err)
	}
	data = quotaFile{Version: 1, Epoch: hex.EncodeToString(epoch[:]), Records: []quotaRecord{}}
	if err := s.save(ctx, target, data); err != nil {
		return "", err
	}
	return data.Epoch, nil
}

// Read never creates a file, directory or lock. An unusable store is unknown,
// accompanied by an error, not an empty allowance or a restored backup.
func (s *QuotaStore) Read(subject QuotaSubject, scope string, now time.Time) (QuotaSnapshot, error) {
	unknown := QuotaSnapshot{Subject: subject, Scope: scope, State: QuotaStateUnknown}
	if err := validateQuotaSubject(subject); err != nil {
		return unknown, err
	}
	if !validQuotaID(scope) || now.IsZero() {
		return unknown, fmt.Errorf("%w: read scope or time", ErrQuotaInvalid)
	}
	target, err := quotaTarget(s.path)
	if err != nil {
		return unknown, err
	}
	data, err := readQuotaFile(target)
	if err != nil {
		return unknown, err
	}
	record := data.record(subject)
	version := QuotaVersion{StoreEpoch: data.Epoch}
	if record != nil {
		version.Revision = record.Revision
	}
	return quotaView(record, subject, scope, version, now), nil
}

// Apply acknowledges only the retained last operation on a subject. A retry is
// checked before wall-time/CAS validation: expiry cannot uncommit an operation.
func (s *QuotaStore) Apply(ctx context.Context, observation QuotaObservation, now time.Time) (QuotaCommit, error) {
	target, release, err := s.lock(ctx)
	if err != nil {
		return QuotaCommit{}, err
	}
	defer release()
	data, err := readQuotaFile(target)
	if err != nil {
		return QuotaCommit{}, err
	}
	if err := validateQuotaObservationStructure(observation); err != nil {
		return QuotaCommit{}, err
	}
	if observation.Expected.StoreEpoch != data.Epoch {
		return QuotaCommit{}, ErrQuotaConflict
	}
	digest, err := quotaDigest(observation)
	if err != nil {
		return QuotaCommit{}, err
	}
	current := data.record(observation.Subject)
	if current != nil && current.LastID == observation.ID {
		if current.Digest != digest {
			return QuotaCommit{}, ErrQuotaConflict
		}
		return QuotaCommit{Version: QuotaVersion{StoreEpoch: data.Epoch, Revision: current.Revision}, Replayed: true}, nil
	}
	revision := uint64(0)
	if current != nil {
		revision = current.Revision
	}
	if observation.Expected.Revision != revision {
		return QuotaCommit{}, ErrQuotaConflict
	}
	next, err := reduceQuota(current, observation, now)
	if err != nil {
		return QuotaCommit{}, err
	}
	if data.CommitSequence == math.MaxUint64 {
		return QuotaCommit{}, ErrQuotaConflict
	}
	next.LastID, next.Digest = observation.ID, digest
	if err := validateQuotaRecord(next); err != nil {
		return QuotaCommit{}, err
	}
	beforeVersion := QuotaVersion{StoreEpoch: data.Epoch}
	if current != nil {
		beforeVersion.Revision = current.Revision
	}
	version := QuotaVersion{StoreEpoch: data.Epoch, Revision: next.Revision}
	transition := &QuotaTransition{
		Before: quotaViews(current, observation.Subject, observation.Coverage, beforeVersion, now),
		After:  quotaViews(&next, observation.Subject, next.Coverage, version, now),
	}
	if current == nil {
		data.Records = append(data.Records, next)
	} else {
		*current = next
	}
	data.CommitSequence++
	if err := s.save(ctx, target, data); err != nil {
		return QuotaCommit{}, err
	}
	return QuotaCommit{Version: version, Transition: transition}, nil
}

func (d *quotaFile) record(subject QuotaSubject) *quotaRecord {
	for i := range d.Records {
		if d.Records[i].Subject == subject {
			return &d.Records[i]
		}
	}
	return nil
}

func (s *QuotaStore) save(ctx context.Context, target string, data quotaFile) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrQuotaBusy, err)
	}
	b, err := json.Marshal(data)
	if err != nil {
		return ErrQuotaCorrupt
	}
	write := s.write
	if write == nil {
		write = steady.WriteFile
	}
	if err := write(target, b, 0600); err != nil {
		return quotaIO("write", err)
	}
	return nil
}

func readQuotaFile(target string) (quotaFile, error) {
	b, err := os.ReadFile(target)
	if errors.Is(err, fs.ErrNotExist) {
		return quotaFile{}, ErrQuotaMissing
	}
	if err != nil {
		return quotaFile{}, quotaIO("read", err)
	}
	return decodeQuotaFile(b)
}

func decodeQuotaFile(b []byte) (quotaFile, error) {
	bad := func() (quotaFile, error) { return quotaFile{}, ErrQuotaCorrupt }
	if !utf8.Valid(b) {
		return bad()
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if err := quotaJSONMembers(decoder, 0); err != nil {
		return bad()
	}
	if _, err := decoder.Token(); err != io.EOF {
		return bad()
	}
	var header map[string]json.RawMessage
	if json.Unmarshal(b, &header) != nil || header == nil {
		return bad()
	}
	var version uint64
	if raw, ok := header["version"]; !ok || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &version) != nil {
		return bad()
	}
	if version != 1 {
		return quotaFile{}, ErrQuotaUnsupported
	}
	if !quotaJSONShape(b, reflect.TypeFor[quotaFile]()) {
		return bad()
	}
	var data quotaFile
	if json.Unmarshal(b, &data) != nil {
		return bad()
	}
	if !validQuotaID(data.Epoch) {
		return bad()
	}
	seen := make(map[QuotaSubject]bool)
	var sequence uint64
	for _, record := range data.Records {
		if seen[record.Subject] || validateQuotaRecord(record) != nil || record.Revision > math.MaxUint64-sequence {
			return bad()
		}
		seen[record.Subject] = true
		sequence += record.Revision
	}
	if sequence != data.CommitSequence {
		return bad()
	}
	return data, nil
}

// encoding/json otherwise accepts duplicate keys and case-insensitive aliases.
// Check all object members before interpreting a schema or exposing any record.
func quotaJSONMembers(d *json.Decoder, depth int) error {
	if depth > 64 {
		return ErrQuotaCorrupt
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return ErrQuotaCorrupt
			}
			seen[name] = true
			if err := quotaJSONMembers(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := quotaJSONMembers(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return ErrQuotaCorrupt
	}
	end, err := d.Token()
	if err != nil || (delim == '{' && end != json.Delim('}')) || (delim == '[' && end != json.Delim(']')) {
		return ErrQuotaCorrupt
	}
	return nil
}

// Require canonical field spelling and presence, including explicit zeroes.
// Optional pointer fields may be absent, but JSON null is not a stored value.
func quotaJSONShape(b []byte, typ reflect.Type) bool {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return false
	}
	if typ.Kind() == reflect.Pointer {
		return quotaJSONShape(b, typ.Elem())
	}
	if typ == reflect.TypeFor[time.Time]() {
		var value time.Time
		return json.Unmarshal(b, &value) == nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(b, &object) != nil || object == nil {
			return false
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
			raw, ok := object[name]
			if !ok {
				if options == "omitempty" {
					continue
				}
				return false
			}
			if !quotaJSONShape(raw, field.Type) {
				return false
			}
			delete(object, name)
		}
		return len(object) == 0
	case reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(b, &values) != nil {
			return false
		}
		for _, value := range values {
			if !quotaJSONShape(value, typ.Elem()) {
				return false
			}
		}
	}
	return true // The final typed decode checks primitive types and ranges.
}

func quotaDigest(o QuotaObservation) (string, error) {
	o.ObservedAt = o.ObservedAt.UTC()
	o.Coverage = append([]QuotaCoverage(nil), o.Coverage...)
	for i := range o.Coverage {
		o.Coverage[i].Scopes = append([]string(nil), o.Coverage[i].Scopes...)
		sort.Strings(o.Coverage[i].Scopes)
	}
	sort.Slice(o.Coverage, func(i, j int) bool { return o.Coverage[i].Window < o.Coverage[j].Window })
	o.Facts = append([]QuotaFact(nil), o.Facts...)
	for i := range o.Facts {
		fact := &o.Facts[i]
		fact.FreshUntil, fact.ResetAt = fact.FreshUntil.UTC(), fact.ResetAt.UTC()
		if fact.Capacity != nil {
			capacity := *fact.Capacity
			if capacity.Remaining == 0 {
				capacity.Remaining = 0
			} // Normalize negative zero.
			fact.Capacity = &capacity
		}
	}
	sort.Slice(o.Facts, func(i, j int) bool { return o.Facts[i].Window < o.Facts[j].Window })
	b, err := json.Marshal(o)
	if err != nil {
		return "", ErrQuotaInvalid
	}
	digest := sha256.Sum256(b)
	return hex.EncodeToString(digest[:]), nil
}

type quotaProcessLock struct {
	gate chan struct{}
	refs int
}

var quotaProcessLocks struct {
	sync.Mutex
	paths map[string]*quotaProcessLock
}

const quotaLockWait = 10 * time.Second

func quotaLocalLock(ctx context.Context, target string) (func(), error) {
	key := target
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		key = strings.ToLower(key)
	}
	quotaProcessLocks.Lock()
	if quotaProcessLocks.paths == nil {
		quotaProcessLocks.paths = make(map[string]*quotaProcessLock)
	}
	entry := quotaProcessLocks.paths[key]
	if entry == nil {
		entry = &quotaProcessLock{gate: make(chan struct{}, 1)}
		quotaProcessLocks.paths[key] = entry
	}
	entry.refs++
	quotaProcessLocks.Unlock()
	drop := func() {
		quotaProcessLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(quotaProcessLocks.paths, key)
		}
		quotaProcessLocks.Unlock()
	}
	select {
	case entry.gate <- struct{}{}:
		return func() { <-entry.gate; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, fmt.Errorf("%w: %w", ErrQuotaBusy, ctx.Err())
	}
}

func (s *QuotaStore) lock(ctx context.Context) (string, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, quotaLockWait)
	defer cancel()
	target, err := quotaTarget(s.path)
	if err != nil {
		return "", nil, err
	}
	localRelease, err := quotaLocalLock(ctx, target)
	if err != nil {
		return "", nil, err
	}
	var file *os.File
	fail := func(err error) (string, func(), error) {
		if file != nil {
			file.Close()
		}
		localRelease()
		return "", nil, err
	}
	if err := ctx.Err(); err != nil {
		return fail(fmt.Errorf("%w: %w", ErrQuotaBusy, err))
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return fail(quotaIO("lock directory", err))
	}
	file, err = os.OpenFile(target+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fail(quotaIO("lock open", err))
	}
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return fail(fmt.Errorf("%w: %w", ErrQuotaBusy, err))
		}
		held, err := filelock.TryLock(file)
		if err != nil {
			return fail(quotaIO("lock ownership", err))
		}
		if held {
			var once sync.Once
			return target, func() { once.Do(func() { filelock.Unlock(file); file.Close(); localRelease() }) }, nil
		}
		select {
		case <-ctx.Done():
		case <-tick.C:
		}
	}
}

// Resolve absent targets through their deepest existing ancestor too. The same
// sibling lock then owns a direct path, a link, and a not-yet-created target.
// Hard-link aliases and concurrent external symlink retargeting are unsupported.
func quotaTarget(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", ErrQuotaInvalid
	}
	target, err := edit.Target(path)
	if err != nil {
		return "", quotaIO("target", err)
	}
	rest := ""
	for range 255 {
		resolved, err := filepath.EvalSymlinks(target)
		if err == nil {
			absolute, err := filepath.Abs(filepath.Join(resolved, rest))
			if err != nil {
				return "", quotaIO("target", err)
			}
			return absolute, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", quotaIO("target", err)
		}
		// An ancestor can itself be a dangling directory link.
		linked, err := edit.Target(target)
		if err != nil {
			return "", quotaIO("target", err)
		}
		if linked != target {
			target = linked
			continue
		}
		// Split without cleaning: a '..' must traverse the resolved directory
		// link, not its lexical parent. Abs and Dir would clean it too soon.
		parent, base := filepath.Split(target)
		if parent == "" {
			parent = "."
		} else {
			for len(parent) > len(filepath.VolumeName(parent))+1 && os.IsPathSeparator(parent[len(parent)-1]) {
				parent = parent[:len(parent)-1]
			}
		}
		if parent == target {
			return "", quotaIO("target", fs.ErrNotExist)
		}
		rest = filepath.Join(base, rest)
		target = parent
	}
	return "", quotaIO("target links", fs.ErrInvalid)
}

// Filesystem paths and decoder input may contain private caller data. Preserve
// error identity without rendering a path or raw persisted value in the error.
func quotaIO(operation string, err error) error {
	return &quotaIOError{operation: operation, cause: err}
}

type quotaIOError struct {
	operation string
	cause     error
}

func (e *quotaIOError) Error() string   { return ErrQuotaIO.Error() + ": " + e.operation }
func (e *quotaIOError) Unwrap() []error { return []error{ErrQuotaIO, e.cause} }
