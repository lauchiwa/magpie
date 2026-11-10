package usage

import (
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

var ErrBudgetTime = errors.New("invalid scoped reservation clock")

// BudgetTime preserves historical request time; scoped sends use their own
// admission time even when a fallback's display row starts before a reset.
func BudgetTime(r Record) (time.Time, error) {
	if !strings.HasPrefix(r.Hold, "s2:") {
		return r.Time, nil
	}
	parts := strings.Split(r.Hold, ":")
	if len(parts) != 3 || len(parts[1]) != 32 {
		return time.Time{}, ErrBudgetTime
	}
	id, err := hex.DecodeString(parts[1])
	if err != nil || hex.EncodeToString(id) != parts[1] {
		return time.Time{}, ErrBudgetTime
	}
	n, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != parts[2] {
		return time.Time{}, ErrBudgetTime
	}
	return time.Unix(0, n), nil
}

func (c *rowChunk) noteBudgetTime(r Record) {
	at, err := BudgetTime(r)
	if err != nil {
		c.BudgetInvalid = true
	} else if at.After(c.BudgetLatest) {
		c.BudgetLatest = at
	}
}

// VisitBudgetRead visits by admission time without first filtering display
// time. A malformed envelope is always passed to fn, which can attribute the
// error to the affected principal. Callers must discard sums on any read error.
func VisitBudgetRead(since time.Time, fn func(Record)) error {
	s := readLogSnapshot()
	if s.err != nil {
		return s.err
	}
	if s.info != nil && !s.complete() {
		return errors.New("usage log read is incomplete")
	}
	return visitBudgetBlocks(s.blocks, since, fn)
}

func visitBudgetBlocks(blocks []*rowChunk, since time.Time, fn func(Record)) error {
	rows := chunkRows{}
	defer rows.release()
	for _, packed := range blocks {
		if !packed.BudgetInvalid && !packed.BudgetLatest.IsZero() && packed.BudgetLatest.Before(since) {
			continue
		}
		c := rows.of(packed)
		if c == nil || len(c.Rows) != packed.Count {
			return errors.New("usage budget block could not be decoded")
		}
		for i := range c.Rows {
			r := c.row(i).Record
			at, err := BudgetTime(r)
			if err != nil || !at.Before(since) {
				fn(r)
			}
		}
	}
	return nil
}
