// parlay client — the operator's door for any client, not one product's
// wiring: admit an origin, revoke it, list what is admitted, and check why a
// given client cannot connect.
//
// The admit list is a file ($PARLAY_STATE_HOME/allowed-origins, one origin per
// line) that the server re-reads on change, so an admission needs neither a
// plist edit nor a restart. PARLAY_ALLOWED_ORIGINS in the server's environment
// still works and is shown separately by `list` (read from the running server,
// since this process cannot see that environment).
//
// `check` is the diagnosis half. It probes the way a browser would — with an
// Origin header — and names exactly one of: unreachable, origin-refused,
// no-cors, stream-blocked, or ok. Read-only: GET/OPTIONS only.
package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// AllowedOriginsFile is the admit list's path under the state directory. It
// is the same file the server's guard reads (guard.AllowedOriginsFileName).
func AllowedOriginsFile() string {
	return filepath.Join(config.StateHome(), "allowed-origins")
}

// Client implements `parlay client admit|revoke|list|check`.
func Client(argv []string) {
	if helpWanted("client", argv) {
		return
	}
	r := args.Parse("client", argv, []string{"--json"}, []string{"--server"})
	if len(r.Positionals) == 0 {
		httpc.Die("parlay client: want a subcommand — admit <origin> | revoke <origin> | list | check <url-or-origin> (see 'parlay client --help')", config.ExitUsage)
		return
	}
	sub, rest := r.Positionals[0], r.Positionals[1:]
	switch sub {
	case "admit", "revoke":
		if len(rest) != 1 {
			httpc.Die(fmt.Sprintf("parlay client %s: exactly one origin, e.g. 'parlay client %s http://100.64.0.7:8787'", sub, sub), config.ExitUsage)
			return
		}
		origin, err := NormalizeOrigin(rest[0])
		if err != nil {
			httpc.Die(fmt.Sprintf("parlay client %s: %v", sub, err), config.ExitUsage)
			return
		}
		changed, err := editAdmitList(AllowedOriginsFile(), origin, sub == "admit")
		if err != nil {
			httpc.Die(fmt.Sprintf("parlay client %s: %v", sub, err), config.ExitRuntime)
			return
		}
		verb := map[bool]string{true: "admitted", false: "revoked"}[sub == "admit"]
		if !changed {
			verb = map[bool]string{true: "already admitted", false: "was not admitted"}[sub == "admit"]
		}
		fmt.Printf("%s: %s (%s)\n", verb, origin, AllowedOriginsFile())
		if sub == "revoke" {
			fmt.Println("note: an origin also named in the server's PARLAY_ALLOWED_ORIGINS stays admitted until that env is changed — 'parlay client list' shows both")
		}
	case "list":
		clientList(r.Bool("--json"))
	case "check":
		if len(rest) != 1 {
			httpc.Die("parlay client check: exactly one <url-or-origin> — the client's page address, e.g. http://100.64.0.7:8787", config.ExitUsage)
			return
		}
		origin, err := NormalizeOrigin(rest[0])
		if err != nil {
			httpc.Die(fmt.Sprintf("parlay client check: %v", err), config.ExitUsage)
			return
		}
		server := config.ServerURL()
		if v, ok := r.String("--server"); ok {
			server = strings.TrimRight(strings.TrimSpace(v), "/")
		}
		res := ClientCheck(context.Background(), &http.Client{Timeout: 6 * time.Second}, server, origin)
		if r.Bool("--json") {
			b, _ := json.MarshalIndent(res, "", "  ")
			fmt.Println(string(b))
		} else {
			fmt.Print(res.Render())
		}
		if res.Verdict != VerdictOK {
			httpc.Exit(config.ExitRuntime)
		}
	default:
		httpc.Die(fmt.Sprintf("parlay client: unknown subcommand %q — admit | revoke | list | check", sub), config.ExitUsage)
	}
}

// NormalizeOrigin reduces a URL or origin to scheme://host[:port]. A path,
// query or userinfo is dropped for a URL a user pasted from their address
// bar, but "*" and "null" are refused: opening the door to every origin is a
// hand edit of the file, not a one-word command.
func NormalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "*" || raw == "null" || raw == "" {
		return "", fmt.Errorf("%q is not an origin — give the client's address, e.g. http://100.64.0.7:8787 (to admit every origin, edit %s by hand and add *)", raw, AllowedOriginsFile())
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("%q is not an http(s) origin", raw)
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), nil
}

// editAdmitList adds or removes one origin, preserving comments and order,
// and reports whether the file changed. The write is a temp file + rename so
// the server never reads a half-written list.
func editAdmitList(path, origin string, add bool) (bool, error) {
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) == 1 && lines[0] == "" {
			lines = nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	present := false
	var kept []string
	for _, line := range lines {
		entry := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if entry == origin {
			present = true
			if !add {
				continue
			}
		}
		kept = append(kept, line)
	}
	if add == present {
		return false, nil
	}
	if add {
		kept = append(kept, origin)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".allowed-origins-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	body := strings.Join(kept, "\n")
	if body != "" {
		body += "\n"
	}
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
}

// clientDoc is the subset of GET /api/chat/client that `list` and `check` use.
type clientDoc struct {
	ContractVersion int    `json:"contractVersion"`
	Listen          string `json:"listen"`
	Exposure        string `json:"exposure"`
	YourOrigin      string `json:"yourOrigin"`
	YourOriginState string `json:"yourOriginState"`
	Admitted        struct {
		File     []string `json:"file"`
		Env      []string `json:"env"`
		FilePath string   `json:"filePath"`
	} `json:"admitted"`
}

func fetchClientDoc(ctx context.Context, hc *http.Client, server, origin string) (*clientDoc, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/chat/client", nil)
	if err != nil {
		return nil, 0, err
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, resp.StatusCode, nil
	}
	var d clientDoc
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("not the onboarding document: %w", err)
	}
	return &d, resp.StatusCode, nil
}

func clientList(asJSON bool) {
	file := readAdmitFile(AllowedOriginsFile())
	server := config.ServerURL()
	doc, _, err := fetchClientDoc(context.Background(), &http.Client{Timeout: 4 * time.Second}, server, "")
	if asJSON {
		out := map[string]any{"server": server, "file": file, "filePath": AllowedOriginsFile(), "serverReachable": doc != nil}
		if doc != nil {
			out["listen"], out["exposure"] = doc.Listen, doc.Exposure
			out["serverFile"], out["serverEnv"] = doc.Admitted.File, doc.Admitted.Env
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Printf("admit list file: %s\n", AllowedOriginsFile())
	printOrigins("  file", file)
	if doc == nil {
		reason := "did not answer"
		if err != nil {
			reason = err.Error()
		}
		fmt.Printf("server %s: %s — the env admissions and listen address are unknown (the file above is what a restarted server would read)\n", server, reason)
		return
	}
	fmt.Printf("server %s listens on %s (exposure: %s)\n", server, doc.Listen, doc.Exposure)
	printOrigins("  env (PARLAY_ALLOWED_ORIGINS, set on the server)", doc.Admitted.Env)
	fmt.Println("  always admitted without listing: loopback / .local / private-LAN origins on every route; tailnet and bare-host origins on the events stream and eval only")
}

func printOrigins(label string, origins []string) {
	if len(origins) == 0 {
		fmt.Printf("%s: (none)\n", label)
		return
	}
	fmt.Printf("%s:\n", label)
	for _, o := range origins {
		fmt.Printf("    %s\n", o)
	}
}

func readAdmitFile(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if e := strings.TrimSpace(strings.SplitN(line, "#", 2)[0]); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// Verdicts are the closed vocabulary `check` answers in.
const (
	VerdictOK            = "ok"
	VerdictUnreachable   = "unreachable"
	VerdictOriginRefused = "origin-refused"
	VerdictNoCORS        = "no-cors"
	VerdictStreamBlocked = "stream-blocked"
)

// ClientCheckStep is one probe and what it saw.
type ClientCheckStep struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// ClientCheckResult is the whole diagnosis. Verdict is the FIRST failing layer, in
// the order a connection fails: reach the server, get past the origin guard,
// get CORS headers back, receive the stream.
type ClientCheckResult struct {
	Server  string            `json:"server"`
	Origin  string            `json:"origin"`
	Verdict string            `json:"verdict"`
	Fix     string            `json:"fix,omitempty"`
	Steps   []ClientCheckStep `json:"steps"`
}

// Render is the human report: one line per step, then the verdict and the fix.
func (c ClientCheckResult) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "client %s -> server %s\n", c.Origin, c.Server)
	for _, s := range c.Steps {
		mark := "ok  "
		if !s.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "  %s %-26s %s\n", mark, s.Name, s.Detail)
	}
	if c.Verdict == VerdictOK {
		b.WriteString("verdict: ok — this origin can connect, send and receive\n")
	} else {
		fmt.Fprintf(&b, "verdict: %s\n", c.Verdict)
		if c.Fix != "" {
			fmt.Fprintf(&b, "fix: %s\n", c.Fix)
		}
	}
	return b.String()
}

// checkProbes are the browser-visible routes a client needs, each probed by
// preflight so the answer is what a real page would get.
var checkProbes = []struct{ name, method, path string }{
	{"preflight send-input", "POST", "/api/chat/eval"},
	{"preflight announce", "POST", "/api/chat/register-agent"},
}

// ClientCheck diagnoses why origin can or cannot use server. It never
// mutates: GET and OPTIONS only.
func ClientCheck(ctx context.Context, hc *http.Client, server, origin string) ClientCheckResult {
	res := ClientCheckResult{Server: server, Origin: origin}
	fail := func(verdict, fix string) ClientCheckResult {
		res.Verdict, res.Fix = verdict, fix
		return res
	}
	add := func(name string, ok bool, format string, a ...any) {
		res.Steps = append(res.Steps, ClientCheckStep{Name: name, OK: ok, Detail: fmt.Sprintf(format, a...)})
	}

	// 1. Reach the server at all, with no Origin (the CLI's own view).
	doc, status, err := fetchClientDoc(ctx, hc, server, origin)
	if err != nil && status == 0 {
		add("reach server", false, "%s", describeNetErr(err))
		return fail(VerdictUnreachable, "start the server, or point the client at the address it listens on. The server's exposure decides who can reach it: loopback (default) is this machine only; set PARLAY_EXPOSURE=lan or tailnet (docs/client-contract.md#exposure) for other machines")
	}
	switch {
	case doc != nil:
		add("reach server", true, "answered; listens on %s (exposure: %s)", doc.Listen, doc.Exposure)
	case status != 0:
		add("reach server", true, "answered HTTP %d on /api/chat/client — an older server without the onboarding endpoint; probing the routes directly", status)
	}

	// 2. The origin guard, on the events stream.
	evReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/chat/events?device=client-check", nil)
	evReq.Header.Set("Origin", origin)
	evReq.Header.Set("Accept", "text/event-stream")
	// A stream has no end; bound the probe separately from hc's timeout.
	sctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	evResp, err := (&http.Client{Transport: hc.Transport}).Do(evReq.WithContext(sctx))
	if err != nil {
		add("events stream", false, "%s", describeNetErr(err))
		return fail(VerdictUnreachable, "the server answered once but the stream request failed; check for a proxy that drops long-lived connections")
	}
	defer evResp.Body.Close()
	admit := fmt.Sprintf("parlay client admit %s", origin)
	if evResp.StatusCode == http.StatusForbidden {
		add("events stream", false, "HTTP 403 — the origin guard refused %s", origin)
		return fail(VerdictOriginRefused, admit)
	}
	if evResp.StatusCode != http.StatusOK {
		add("events stream", false, "HTTP %d", evResp.StatusCode)
		return fail(VerdictStreamBlocked, "the server did not serve the stream; 'parlay health' shows server state")
	}
	add("origin guard", true, "%s is not refused", origin)

	// 3. CORS: a browser reads the response only if ACAO names its origin.
	if acao := evResp.Header.Get("Access-Control-Allow-Origin"); acao != origin {
		got := acao
		if got == "" {
			got = "no Access-Control-Allow-Origin header"
		}
		add("events CORS", false, "stream answered 200 but sent %s — a browser page cannot read it", got)
		return fail(VerdictNoCORS, admit)
	}
	add("events CORS", true, "Access-Control-Allow-Origin echoes %s", origin)

	// 4. The stream itself: a proxy that buffers delivers headers and then
	// nothing, which looks exactly like a working connection until you wait.
	if line, ok := firstEventLine(sctx, evResp.Body); ok {
		add("events stream flows", true, "first frame: %s", line)
	} else {
		add("events stream flows", false, "headers arrived but no event within 4s — something between here and the server is buffering the stream")
		return fail(VerdictStreamBlocked, "disable response buffering on the proxy for /api/chat/events (nginx: proxy_buffering off), or connect to the server directly")
	}

	// 5. Preflight the routes a non-observing client also needs.
	for _, p := range checkProbes {
		req, _ := http.NewRequestWithContext(ctx, http.MethodOptions, server+p.path, nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", p.method)
		req.Header.Set("Access-Control-Request-Headers", "content-type")
		resp, err := hc.Do(req)
		if err != nil {
			add(p.name, false, "%s", describeNetErr(err))
			return fail(VerdictUnreachable, "the server stopped answering mid-check")
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusForbidden:
			add(p.name, false, "HTTP 403 — %s %s refuses this origin (only the events stream and eval accept tailnet/LAN origins unlisted)", p.method, p.path)
			return fail(VerdictOriginRefused, admit)
		case resp.Header.Get("Access-Control-Allow-Origin") != origin:
			add(p.name, false, "HTTP %d with no Access-Control-Allow-Origin for this origin", resp.StatusCode)
			return fail(VerdictNoCORS, admit)
		default:
			add(p.name, true, "%s %s allowed", p.method, p.path)
		}
	}
	res.Verdict = VerdictOK
	return res
}

// firstEventLine returns the first "event:"/"data:" line of an SSE body, or
// false if none arrives before ctx ends.
func firstEventLine(ctx context.Context, body io.Reader) (string, bool) {
	ch := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(body)
		for sc.Scan() {
			t := sc.Text()
			if strings.HasPrefix(t, "event:") || strings.HasPrefix(t, "data:") {
				ch <- t
				return
			}
		}
		ch <- ""
	}()
	select {
	case t := <-ch:
		if t == "" {
			return "", false
		}
		if len(t) > 60 {
			t = t[:60] + "…"
		}
		return t, true
	case <-ctx.Done():
		return "", false
	}
}

func describeNetErr(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return "timed out — nothing answered (wrong address, a firewall, or the server is bound to a different interface)"
	case strings.Contains(err.Error(), "connection refused"):
		return "connection refused — nothing is listening there (server down, wrong port, or it is bound to loopback only)"
	}
	return err.Error()
}
