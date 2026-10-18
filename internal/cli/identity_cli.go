package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/gateway"
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
	case "reconcile":
		return identityReconcile(rest[1:])
	}
	return fmt.Errorf("usage: magpie identity [ls|import|arm|disarm|confirm|bind|unbind|rm|reconcile]")
}

func identityList() error {
	// profiles and bindings from one read (lifecycle_fork.go)
	view, err := provider.ReadIdentityView(false)
	if err != nil {
		return err
	}
	ps, bound := view.Snapshot.Profiles(), view.Snapshot.Bindings()
	defer identityArmedNote()
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

// identityArmedNote says which providers have a live capture window on
// the running gateway. A window is memory-only and otherwise invisible,
// so without this an operator cannot tell an arm from a no-op. A gateway
// that isn't running has none, and says nothing.
func identityArmedNote() {
	if !gateway.Running() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	armed, err := gateway.ArmedIdentities(ctx, gateway.URL())
	if err != nil || len(armed) == 0 {
		return
	}
	ids := make([]string, 0, len(armed))
	for _, item := range armed {
		label := item.Provider
		if item.Client != "" {
			label += " (" + string(item.Client) + ")"
		}
		if item.Expires != nil {
			label += " until " + item.Expires.Local().Format("15:04:05")
		}
		ids = append(ids, label)
	}
	fmt.Printf("capture armed on the gateway for: %s\n", strings.Join(ids, ", "))
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

// A capture window lives in the memory of the process that forwards the
// requests, which is the gateway's, not this command's. Arming here would
// arm a window nothing consults and that dies when the command returns, so
// arm and disarm ask the running gateway over its local control paths
// (internal/gateway/identity.go). With no gateway running there is nothing
// to arm, and that is said rather than reported as armed.
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
	if !client.Valid() {
		return fmt.Errorf("no such client %q: codex_cli or claude_code", args[1])
	}
	if !gateway.Running() {
		return fmt.Errorf("no gateway runs here, and a capture window belongs to the gateway that forwards the requests: start one (the app, magpie web or magpie serve), then arm it")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := gateway.ArmIdentity(ctx, gateway.URL(), p.ID, client); err != nil {
		return err
	}
	fmt.Printf("armed a one-shot %s capture for %s on the gateway; it expires in %s and creates an unconfirmed candidate\n", client, p.Name, identity.CaptureWindow)
	fmt.Printf("send one request through %s with %s now, then: magpie identity ls\n", p.Name, client)
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
	if !gateway.Running() {
		return fmt.Errorf("no gateway runs here, and a capture window belongs to the gateway that forwards the requests: start one (the app, magpie web or magpie serve), then disarm it")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := gateway.DisarmIdentity(ctx, gateway.URL(), p.ID); err != nil {
		return err
	}
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
	// bound as the provider read here: renamed since, under its new id;
	// deleted or made anew, refused. BindIdentity says first, rather than
	// the first request, that a profile bound to an account or a plugin's
	// provider would be read and never sent
	between("bind")
	if err := provider.BindIdentity(*p, args[1]); err != nil {
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
		return unbindOrphan(args[0], err)
	}
	between("unbind")
	if err := provider.UnbindIdentity(*p); err != nil {
		return err
	}
	fmt.Printf("%s presents no identity profile\n", p.Name)
	return nil
}

// unbindOrphan removes the binding a provider no longer stored left under
// id (deleted, or bound by hand): there is no provider to unbind it as.
// With no binding there either, notFound is the answer; bindings that can't
// be read are said, never taken for none.
func unbindOrphan(id string, notFound error) error {
	id = strings.ToLower(strings.TrimSpace(id))
	bound, err := identity.Bindings()
	if err != nil {
		return fmt.Errorf("%w; whether a binding is left under %q is unknown: %v", notFound, id, err)
	}
	if _, ok := bound[id]; !ok {
		return notFound
	}
	between("unbind")
	if err := provider.UnbindOrphan(id); err != nil {
		return err
	}
	fmt.Printf("no provider %s is stored; the binding left under it is removed\n", id)
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

const reconcileUsage = "usage: magpie identity reconcile [--yes [--keep|--drop]]"

// identityReconcile is `magpie identity reconcile`: lifecycle.json put in
// step with providers.json after a change magpie didn't make (an older
// magpie, an editor, a providers.json read from its .bak). Alone it shows
// each provider id's instance and the bindings left undecided, and writes
// nothing. --yes writes, keeping the old lifecycle.json (and a journal
// that couldn't be finished) aside first; with undecided bindings it also
// needs --keep (leave them) or --drop (unbind them). Any other argument
// is refused before anything is read.
func identityReconcile(args []string) error {
	yes, keep, drop := false, false, false
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--keep":
			keep = true
		case "--drop":
			drop = true
		default:
			return fmt.Errorf("%s: unknown argument %q", reconcileUsage, a)
		}
	}
	if keep && drop {
		return fmt.Errorf("%s: --keep and --drop can't both be given", reconcileUsage)
	}
	if !yes {
		plan, err := provider.PreviewReconcile()
		if err != nil {
			return err
		}
		printReconcile(plan)
		if plan.Pending != "" || plan.State != "legacy" && plan.State != "current" || len(plan.Affected) > 0 {
			switch {
			case len(plan.Affected) > 0:
				fmt.Println("nothing was written. To write it: magpie identity reconcile --yes --keep (leave these bindings) or --drop (unbind them)")
			default:
				fmt.Println("nothing was written. To write it: magpie identity reconcile --yes")
			}
		}
		return nil
	}
	choice := provider.ReconcileUndecided
	switch {
	case keep:
		choice = provider.ReconcileKeep
	case drop:
		choice = provider.ReconcileDrop
	}
	plan, err := provider.Reconcile(choice)
	for _, k := range plan.Kept {
		fmt.Printf("kept %s\n", k)
	}
	if errors.Is(err, provider.ErrReconcileChoice) {
		printReconcile(plan)
		return fmt.Errorf("nothing was written: %d binding(s) are undecided; add --keep to leave them or --drop to unbind them", len(plan.Affected))
	}
	if err != nil {
		return err
	}
	printReconcile(plan)
	if plan.Wrote {
		fmt.Println("reconciled")
	}
	return nil
}

func printReconcile(plan provider.ReconcilePlan) {
	if plan.Pending != "" {
		fmt.Printf("an operation is pending in %s: --yes finishes it, or keeps it aside when its files no longer match it\n", plan.Pending)
	}
	switch plan.State {
	case "legacy":
		fmt.Println("no lifecycle.json: providers are matched to their bindings by id, and there is nothing to reconcile")
		return
	case "current":
		if len(plan.Affected) == 0 {
			fmt.Println("lifecycle.json matches providers.json")
		}
	case "adopt":
		fmt.Println("providers.json was changed outside magpie; lifecycle.json would take it as below")
	case "corrupt":
		fmt.Println("lifecycle.json is corrupt; --yes keeps it aside and gives every provider a new instance (requests read before then are refused once)")
	case "fallback":
		fmt.Println("providers.json can't be read and its .bak is read in its place; fix it, or save any provider change, then reconcile")
	case "missing":
		fmt.Println("providers.json is not there; add a provider (or put it back), then reconcile")
		return
	}
	for _, r := range plan.Rows {
		inst := r.Instance
		switch {
		case r.Change == "deleted":
			inst = "(retired)"
		case inst == "":
			inst = "(new instance)"
		case len(inst) > 12:
			inst = inst[:12]
		}
		change := ""
		if r.Change != "" {
			change = "  " + r.Change + " outside magpie"
		}
		fmt.Printf("  %-24s %s%s\n", r.ID, inst, change)
	}
	if len(plan.Affected) == 0 {
		return
	}
	fmt.Println("undecided bindings (their requests are refused until reconciled):")
	for _, a := range plan.Affected {
		why := "added under an id this binding was left on"
		if a.Gone {
			why = "removed from providers.json while bound"
		}
		fmt.Printf("  %-24s %s  %s\n", a.ID, a.Profile, why)
	}
}
