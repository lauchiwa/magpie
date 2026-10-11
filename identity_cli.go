package main

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/provider"
)

// identityCmd is `magpie identity …`: the outbound client fingerprints a
// provider can present, and which provider presents which.
//
// A profile is imported from a request a real client sent, never typed in
// field by field: a real client's User-Agent, version and companion
// headers agree with each other, and `import` keeps only the closed set
// for that client (internal/identity). An imported profile is a candidate
// until `confirm`, and only a confirmed one can be bound.
func identityCmd(args []string) error {
	rest := args[1:]
	if len(rest) == 0 {
		return identityList()
	}
	switch rest[0] {
	case "ls", "list":
		return identityList()
	case "import":
		return identityImport(rest[1:])
	case "arm":
		return identityArm(rest[1:])
	case "disarm":
		return identityDisarm(rest[1:])
	case "confirm":
		return identityConfirm(rest[1:])
	case "bind":
		return identityBind(rest[1:])
	case "unbind":
		return identityUnbind(rest[1:])
	case "rm", "remove":
		return identityRemove(rest[1:])
	}
	return fmt.Errorf("usage: magpie identity [ls|import|arm|disarm|confirm|bind|unbind|rm]")
}

func identityList() error {
	ps, err := identity.Profiles()
	if err != nil {
		return err
	}
	bound, err := identity.Bindings()
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		fmt.Println("no identity profiles. Import one from a request a client sent:")
		fmt.Println("  magpie identity import codex_cli <headers-file>")
		fmt.Println("where the file holds that request's headers, one \"Name: value\" a line")
		return nil
	}
	by := map[string][]string{}
	for prov, id := range bound {
		by[id] = append(by[id], prov)
	}
	for _, p := range ps {
		state := "candidate (not sent until confirmed)"
		if p.ConfirmedAt != nil {
			state = "confirmed " + p.ConfirmedAt.Format(time.RFC3339)
		}
		fmt.Printf("%s  %s  %s  %s\n", p.ID, p.Client, p.Source, state)
		fmt.Printf("    user-agent: %s\n", p.Fingerprint.UserAgent)
		fmt.Printf("    version:    %s\n", p.Fingerprint.Version)
		for _, name := range sortedKeys(p.Fingerprint.Headers) {
			fmt.Printf("    %s: %s\n", name, p.Fingerprint.Headers[name])
		}
		if provs := by[p.ID]; len(provs) > 0 {
			sort.Strings(provs)
			fmt.Printf("    presented by: %s\n", strings.Join(provs, ", "))
		}
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// identityImport reads a request's headers from a file and stores the
// fingerprint they project to as a candidate. The file is what the client
// really sent — copied from its own logs, or from a request this magpie
// served — so the values agree with each other rather than being invented.
func identityImport(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: magpie identity import <codex_cli|claude_code> <headers-file>\n  the file holds the request's headers, one \"Name: value\" a line")
	}
	client := identity.Client(args[0])
	if !client.Valid() {
		return fmt.Errorf("no such client %q: codex_cli or claude_code", args[0])
	}
	b, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	h, err := parseHeaderFile(string(b))
	if err != nil {
		return err
	}
	id, err := identity.NewID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	// the request was sent before it was read; the observation precedes
	// the snapshot by definition
	p, err := identity.ImportedCandidate(id, client, h, now)
	if err != nil {
		return err
	}
	if err := identity.Add(p); err != nil {
		return err
	}
	fmt.Printf("imported %s as %s\n", client, p.ID)
	fmt.Printf("  user-agent: %s\n", p.Fingerprint.UserAgent)
	fmt.Printf("  version:    %s\n", p.Fingerprint.Version)
	for _, name := range sortedKeys(p.Fingerprint.Headers) {
		fmt.Printf("  %s: %s\n", name, p.Fingerprint.Headers[name])
	}
	fmt.Printf("it is a candidate and is sent to nobody. Check the fields above, then:\n  magpie identity confirm %s\n", p.ID)
	return nil
}

// parseHeaderFile reads "Name: value" lines. A blank line and a #
// comment are skipped, so a pasted request's blank line before its body
// ends the headers rather than failing.
func parseHeaderFile(s string) (http.Header, error) {
	h := http.Header{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			return nil, fmt.Errorf("not a header line: %q", line)
		}
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if name == "" {
			return nil, fmt.Errorf("not a header line: %q", line)
		}
		h.Add(name, value)
	}
	if len(h) == 0 {
		return nil, fmt.Errorf("no headers in the file")
	}
	return h, nil
}

func identityArm(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: magpie identity arm <provider> <codex_cli|claude_code>")
	}
	p, err := provider.Find(args[0])
	if err != nil {
		return err
	}
	if why := p.IdentityApplies(); why != "" {
		return fmt.Errorf("%s: %s", p.Name, why)
	}
	client := identity.Client(args[1])
	if err := identity.Arm(p.ID, client, time.Now().UTC()); err != nil {
		return err
	}
	fmt.Printf("armed a one-shot %s capture for %s; it expires in %s and creates an unconfirmed candidate\n", client, p.Name, identity.CaptureWindow)
	return nil
}

func identityDisarm(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: magpie identity disarm <provider>")
	}
	p, err := provider.Find(args[0])
	if err != nil {
		return err
	}
	identity.Disarm(p.ID)
	fmt.Printf("disarmed identity capture for %s\n", p.Name)
	return nil
}
func identityConfirm(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: magpie identity confirm <profile-id>")
	}
	if err := identity.Confirm(args[0], time.Now().UTC()); err != nil {
		return err
	}
	fmt.Printf("confirmed %s. Bind it to a provider:\n  magpie identity bind <provider> %s\n", args[0], args[0])
	return nil
}

func identityBind(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: magpie identity bind <provider> <profile-id>")
	}
	p, err := provider.Find(args[0])
	if err != nil {
		return err
	}
	// said here rather than at the first request: a profile bound to an
	// account or a plugin's provider would be read and never sent
	if why := p.IdentityApplies(); why != "" {
		return fmt.Errorf("%s: %s", p.Name, why)
	}
	if err := identity.Bind(p.ID, args[1]); err != nil {
		return err
	}
	fmt.Printf("%s now presents %s on every request magpie makes to it\n", p.Name, args[1])
	return nil
}

func identityUnbind(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: magpie identity unbind <provider>")
	}
	p, err := provider.Find(args[0])
	if err != nil {
		return err
	}
	if err := identity.Unbind(p.ID); err != nil {
		return err
	}
	fmt.Printf("%s presents no identity profile\n", p.Name)
	return nil
}

func identityRemove(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: magpie identity rm <profile-id>")
	}
	if err := identity.Remove(args[0]); err != nil {
		return err
	}
	fmt.Printf("removed %s, and every binding to it\n", args[0])
	return nil
}
