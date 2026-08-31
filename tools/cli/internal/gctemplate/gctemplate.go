// Package gctemplate synthesises Gas City agent templates from parlay launch
// specs (spawn-lift unit 4, epic task-4cfpv.9).
//
// A parlay launch spec is the surface bin/parlay-spawn takes today: agent id,
// display name, color, initial prompt, cwd, model, ccjuggler account. The
// synthesiser turns one into a Pack Spec 2.0 agent directory —
// agents/<id>/agent.toml + prompt.template.md — for the parlay pack inside
// the city scaffold (internal/cityscaffold). The city imports that pack under
// binding "parlay", so a synthesised agent <id> is addressed as parlay.<id>:
//
//	gc session new parlay.<id> --json --no-attach
//
// Output is deterministic byte-for-byte for a given spec (golden-file
// tested): fixed field order, no timestamps, no environment leakage. Nothing
// here runs gc or starts anything — synthesis writes inert files; whichever
// seam calls `gc session new` owns the start.
package gctemplate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// LaunchSpec is parlay's launch surface, normalised. ID is required and must
// be a valid Gas City session identifier (pack spec §agents: ASCII letter or
// digit first, then letters/digits/hyphens/underscores — parlay's kebab ids
// already satisfy this). Everything else is optional.
type LaunchSpec struct {
	ID     string
	Name   string
	Color  string
	Prompt string
	Cwd    string
	Model  string
	// Account is the ccjuggler account the agent should run under. It rides
	// along as env for the session (the spawn path's account resolution reads
	// it); synthesis must not resolve tokens itself.
	Account string

	// StartCommand/Args override the claude provider command — the
	// verification escape hatch (a sandboxed `gc session new` should start
	// something inert, not a real claude), kept because later seams need the
	// same knob. When StartCommand is set, prompt_mode becomes "none": an
	// arbitrary command has no defined prompt argument.
	StartCommand string
	Args         []string
}

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// Synthesize renders the agent template for spec: a map of file paths
// relative to the pack root (agents/<id>/...) to file contents.
func Synthesize(spec LaunchSpec) (map[string][]byte, error) {
	if !sessionIDRe.MatchString(spec.ID) {
		return nil, fmt.Errorf("launch spec id %q is not a valid Gas City session identifier ([A-Za-z0-9][A-Za-z0-9_-]*)", spec.ID)
	}

	start := "claude"
	promptMode := "arg"
	args := []string{"--dangerously-skip-permissions"}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.StartCommand != "" {
		start = spec.StartCommand
		promptMode = "none"
		args = spec.Args
	}

	var b strings.Builder
	b.WriteString("# Synthesised by parlay from a launch spec (internal/gctemplate) — do not\n")
	b.WriteString("# hand-edit; change the spec and re-synthesise. Spawn-lift unit 4, epic\n")
	b.WriteString("# task-4cfpv.9. Field semantics: Pack Spec 2.0 (pinned Gas City ref).\n")
	name := spec.Name
	if name == "" {
		name = spec.ID
	}
	fmt.Fprintf(&b, "description = %s\n", tomlString("parlay agent "+name+" (launch-spec synthesis)"))
	if spec.Cwd != "" {
		fmt.Fprintf(&b, "work_dir = %s\n", tomlString(spec.Cwd))
	}
	// Template presence must never mean autostart: the reconciler skips
	// suspended agents, and an explicit `gc session new` still works. Same
	// doctrine as the city's suspended_on_start = true.
	b.WriteString("suspended = true\n")
	fmt.Fprintf(&b, "start_command = %s\n", tomlString(start))
	fmt.Fprintf(&b, "args = %s\n", tomlStringArray(args))
	fmt.Fprintf(&b, "prompt_mode = %s\n", tomlString(promptMode))
	fmt.Fprintf(&b, "process_names = %s\n", tomlStringArray([]string{filepath.Base(start)}))

	env := map[string]string{"PARLAY_AGENT_ID": spec.ID}
	if spec.Name != "" {
		env["PARLAY_AGENT_NAME"] = spec.Name
	}
	if spec.Color != "" {
		env["PARLAY_AGENT_COLOR"] = spec.Color
	}
	if spec.Account != "" {
		env["PARLAY_SPAWN_DEFAULT_ACCOUNT"] = spec.Account
	}
	b.WriteString("\n[env]\n")
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, tomlString(env[k]))
	}

	var p strings.Builder
	fmt.Fprintf(&p, "You are parlay agent `%s`", spec.ID)
	if spec.Name != "" {
		fmt.Fprintf(&p, " (%s)", spec.Name)
	}
	p.WriteString(", running as a Gas City session.\n")
	p.WriteString("Enroll with the parlay relay first: run `parlay doctor`, then arm your\n")
	p.WriteString("channel with `parlay listen --agent " + spec.ID + "` via your harness Monitor.\n")
	if spec.Prompt != "" {
		p.WriteString("\n## Task\n\n")
		p.WriteString(spec.Prompt)
		if !strings.HasSuffix(spec.Prompt, "\n") {
			p.WriteString("\n")
		}
	}

	base := "agents/" + spec.ID
	return map[string][]byte{
		base + "/agent.toml":         []byte(b.String()),
		base + "/prompt.template.md": []byte(p.String()),
	}, nil
}

// WriteInto synthesises spec and writes the files under packDir (the parlay
// pack root, e.g. <scaffold>/packs/parlay). Existing files for the same
// agent are overwritten — re-synthesising a spec is reconciliation, like the
// scaffold itself. Returns the written paths, sorted.
func WriteInto(packDir string, spec LaunchSpec) ([]string, error) {
	files, err := Synthesize(spec)
	if err != nil {
		return nil, err
	}
	var written []string
	for rel, data := range files {
		dest := filepath.Join(packDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, fmt.Errorf("synthesise agent template: %w", err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return nil, fmt.Errorf("synthesise agent template: %w", err)
		}
		written = append(written, dest)
	}
	sort.Strings(written)
	return written, nil
}

// tomlString renders s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func tomlStringArray(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = tomlString(s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
