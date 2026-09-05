package spawn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// profileEntry is the subset of packages/spawn-profiles/profiles.toml's
// [[profile]] fields resolve_profile()/list_profiles() actually read
// (bin/parlay-spawn:412-551). Other fields (args, prompt_mode, env, ...) are
// consumed by the herdr/gc launch templates, not by this port's spawn
// pipeline, and are left unparsed — go-toml/v2 ignores unknown fields.
type profileEntry struct {
	Name  string `toml:"name"`
	Kind  string `toml:"kind"`
	Model string `toml:"model"`
}

type profilesFile struct {
	Profile []profileEntry `toml:"profile"`
}

// profilesTomlPath mirrors bash's `${PARLAY_SPAWN_PROFILES_TOML:-$REPO_DIR/packages/spawn-profiles/profiles.toml}`.
// REPO_DIR there is derived from the script's own on-disk location (bin/'s
// parent); a compiled Go binary has no equivalent fixed location, so this
// walks up from the process's cwd looking for the catalog, then from the
// running executable's resolved location — either finds the parlay checkout
// this binary shipped with, in the common case (dev "go run" from inside the
// repo, or a build placed under the checkout). Delegated decision (no bash
// equivalent to follow bug-for-bug): documented in the PR body.
func profilesTomlPath() (string, error) {
	if v := os.Getenv("PARLAY_SPAWN_PROFILES_TOML"); v != "" {
		return v, nil
	}
	const rel = "packages/spawn-profiles/profiles.toml"
	if p, ok := findUpward(os.Getenv("PWD"), rel); ok {
		return p, nil
	}
	if cwd, err := os.Getwd(); err == nil {
		if p, ok := findUpward(cwd, rel); ok {
			return p, nil
		}
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := realpath(exe); err == nil {
			if p, ok := findUpward(resolved, rel); ok {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("no profiles.toml found (looked for %s from cwd and from the parlay executable's location; set PARLAY_SPAWN_PROFILES_TOML to override)", rel)
}

func loadProfiles(tomlPath string) (profilesFile, error) {
	var pf profilesFile
	data, err := os.ReadFile(tomlPath)
	if err != nil {
		return pf, fmt.Errorf("--profile: no profiles.toml at %s", tomlPath)
	}
	if err := toml.Unmarshal(data, &pf); err != nil {
		return pf, fmt.Errorf("--profile: %s is not valid TOML: %w", tomlPath, err)
	}
	return pf, nil
}

// resolveProfile mirrors bash's resolve_profile() (lines 412-447): looks up
// name in profiles.toml and returns (kind, model). Errors mirror bash's exit-2
// refusals (missing catalog, unknown profile name) — a typo'd profile must
// not silently fall through to require_model's generic "no model" message.
func resolveProfile(name string) (kind, model string, err error) {
	tomlPath, err := profilesTomlPath()
	if err != nil {
		return "", "", fmt.Errorf("--profile %s — %w", name, err)
	}
	pf, err := loadProfiles(tomlPath)
	if err != nil {
		return "", "", err
	}
	for _, p := range pf.Profile {
		if p.Name == name {
			return p.Kind, p.Model, nil
		}
	}
	return "", "", fmt.Errorf("--profile '%s' not found in %s", name, tomlPath)
}

// quotaWindow/quotaProvider/quotaReport mirror the subset of `quota-axi
// --json`'s shape list_profiles()'s python reads (bin/parlay-spawn:495-524).
type quotaWindow struct {
	Kind             string `json:"kind"`
	PercentRemaining *int   `json:"percentRemaining"`
	ResetsAt         string `json:"resetsAt"`
	Label            string `json:"label"`
	ID               string `json:"id"`
}

type quotaProvider struct {
	Provider string        `json:"provider"`
	Windows  []quotaWindow `json:"windows"`
}

type quotaReport struct {
	Providers []quotaProvider `json:"providers"`
}

// kindToQuotaProvider mirrors bash's KIND_TO_PROVIDER map (line 497).
var kindToQuotaProvider = map[string]string{
	"claude":   "claude",
	"opencode": "opencode-go",
	"codex":    "codex",
}

// fetchQuotaReport best-effort shells to `quota-axi --json`, matching bash's
// degrade-never-fail contract (lines 467-478): a missing binary or a nonzero
// exit both return (nil, note) rather than an error, and the caller renders
// the static catalog with note appended.
func fetchQuotaReport() (*quotaReport, string) {
	if _, err := exec.LookPath("quota-axi"); err != nil {
		return nil, "quota-axi not found on PATH — showing static catalog only."
	}
	var out bytes.Buffer
	cmd := exec.Command("quota-axi", "--json")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, "quota-axi errored — showing static catalog only."
	}
	var q quotaReport
	if err := json.Unmarshal(out.Bytes(), &q); err != nil {
		return nil, "quota-axi output was unreadable — showing static catalog only."
	}
	return &q, ""
}

// headroomLine mirrors bash's headroom() (lines 508-524): the weekly window
// (or the first window if none is tagged "weekly") for kind's mapped
// provider, formatted as "<label> <pct>% remaining, resets HH:MM".
func headroomLine(q *quotaReport, kind string) string {
	if q == nil {
		return ""
	}
	provider := kindToQuotaProvider[kind]
	if provider == "" {
		return ""
	}
	var prov *quotaProvider
	for i := range q.Providers {
		if q.Providers[i].Provider == provider {
			prov = &q.Providers[i]
			break
		}
	}
	if prov == nil || len(prov.Windows) == 0 {
		return ""
	}
	win := prov.Windows[0]
	for _, w := range prov.Windows {
		if w.Kind == "weekly" {
			win = w
			break
		}
	}
	if win.PercentRemaining == nil {
		return ""
	}
	label := win.Label
	if label == "" {
		label = win.ID
	}
	line := fmt.Sprintf("%s %d%% remaining", label, *win.PercentRemaining)
	if len(win.ResetsAt) >= 16 {
		line += fmt.Sprintf(", resets %s", win.ResetsAt[11:16])
	}
	return line
}

// listProfiles mirrors bash's list_profiles() (lines 449-551): renders the
// profiles.toml catalog as a table (NAME, KIND, MODEL, ACCOUNT, +QUOTA when
// available) to stdout, with account defaulting to "(ambient session token)"
// and model to "(none — cannot satisfy the model gate)" when absent — never
// spawns anything.
func listProfiles(w *os.File, account string) error {
	tomlPath, err := profilesTomlPath()
	if err != nil {
		return fmt.Errorf("--list: %w", err)
	}
	pf, err := loadProfiles(tomlPath)
	if err != nil {
		return err
	}

	quota, note := fetchQuotaReport()

	type row struct{ name, kind, model, acct, quota string }
	var rows []row
	for _, p := range pf.Profile {
		model := p.Model
		if model == "" {
			model = "(none — cannot satisfy the model gate)"
		}
		acct := account
		if acct == "" {
			acct = "(ambient session token)"
		}
		rows = append(rows, row{p.Name, p.Kind, model, acct, headroomLine(quota, p.Kind)})
	}

	headers := []string{"NAME", "KIND", "MODEL", "ACCOUNT"}
	widths := make([]int, 4)
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		cols := []string{r.name, r.kind, r.model, r.acct}
		for i, c := range cols {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}

	haveQuota := quota != nil && len(quota.Providers) > 0
	headerLine := padJoin(headers, widths)
	if haveQuota {
		headerLine += "  QUOTA"
	}
	fmt.Fprintln(w, headerLine)
	for _, r := range rows {
		line := padJoin([]string{r.name, r.kind, r.model, r.acct}, widths)
		if r.quota != "" {
			line += "  " + r.quota
		}
		fmt.Fprintln(w, line)
	}

	if note != "" {
		fmt.Fprintf(os.Stderr, "\n%s\n", note)
	}
	return nil
}

func padJoin(cols []string, widths []int) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = c + strings.Repeat(" ", widths[i]-len(c))
	}
	return strings.Join(parts, "  ")
}

// findUpward walks from start up through parent directories looking for a
// file at <dir>/rel, returning the first hit.
func findUpward(start, rel string) (string, bool) {
	if start == "" {
		return "", false
	}
	dir := start
	for {
		candidate := filepath.Join(dir, rel)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
