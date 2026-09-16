// Package gctemplate synthesises Gas City agent templates from parlay launch
// specs (spawn-lift unit 4, epic task-4cfpv.9).
//
// A parlay launch spec is the surface `parlay spawn` takes today: agent id,
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
	// Kind is the agent harness to launch ("claude" or "pi"). Empty
	// means "claude" — the spawn pipeline normalises it before the gc
	// launcher ever sees the spec, and Synthesize defaults it the same way
	// so a direct gc-spawn invocation without --kind keeps rendering the
	// claude template byte-for-byte.
	Kind string
	// Account is the ccjuggler account the agent should run under. It rides
	// along as env for the session (the spawn path's account resolution reads
	// it); synthesis must not resolve tokens itself.
	Account string
	// Server is the parlay server base URL the agent should talk to. It rides
	// along as PARLAY_SERVER env — the gc launcher's equivalent of the
	// subprocess launcher's `--env PARLAY_SERVER=…`. Never a secret: the
	// template env is persisted to disk, so credentials (OAuth tokens) must
	// NEVER travel this way.
	Server string

	// StartCommand/Args override the claude provider command — the
	// verification escape hatch (a sandboxed `gc session new` should start
	// something inert, not a real claude), kept because later seams need the
	// same knob. When StartCommand is set, prompt_mode becomes "none": an
	// arbitrary command has no defined prompt argument. Args are folded into
	// the rendered start_command as one shell-quoted command line — gc's
	// agent-level start_command is "the complete command line"
	// (internal/config/resolve.go at the pin resolves it as an escape hatch
	// that ignores any separate args field).
	StartCommand string
	Args         []string
}

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// gcKinds names the harnesses the synthesiser (and the gc launcher above
// it) knows how to start. Anything else is a loud refusal, not a guessed
// command line — a harness launched with the wrong flags is a broken
// agent, and the spawn pipeline's kind-agnostic launchers (herdr,
// subprocess) are the escape hatch for the rest.
var gcKinds = []string{"claude", "pi"}

// kindStart renders the provider command for one kind: the executable, the
// launch argv, and the prompt delivery mode (the "arg"/"flag"/"none"
// vocabulary of the pinned gc's internal/config/provider.go — the default
// delivery appends the rendered startup prompt as a quoted argv suffix,
// which both harnesses accept positionally). claude gets the YOLO flag set
// the herdr and subprocess launchers pass; every other kind gets only an
// explicit --model and relies on its own config for permissions/fallback —
// the same per-kind split as agentStartArgs in internal/spawn (which pi
// must satisfy twice over: it takes its own --model flag, never claude's
// --dangerously-skip-permissions/--fallback-model/--strict-mcp-config set).
func kindStart(kind, model string) (start string, args []string, promptMode string, err error) {
	if kind == "" {
		kind = "claude"
	}
	switch kind {
	case "claude":
		args = []string{"--dangerously-skip-permissions"}
		if model != "" {
			args = append(args, "--model", model)
		}
		return "claude", args, "arg", nil
	case "pi":
		if model != "" {
			args = append(args, "--model", model)
		}
		return "pi", args, "arg", nil
	default:
		return "", nil, "", fmt.Errorf("unsupported gc kind %q (supported: %s) — use herdr or --subprocess", kind, strings.Join(gcKinds, ", "))
	}
}

// Synthesize renders the agent template for spec: a map of file paths
// relative to the pack root (agents/<id>/...) to file contents.
func Synthesize(spec LaunchSpec) (map[string][]byte, error) {
	if !sessionIDRe.MatchString(spec.ID) {
		return nil, fmt.Errorf("launch spec id %q is not a valid Gas City session identifier ([A-Za-z0-9][A-Za-z0-9_-]*)", spec.ID)
	}

	start, args, promptMode, err := kindStart(spec.Kind, spec.Model)
	if err != nil {
		return nil, err
	}
	if spec.StartCommand != "" {
		start = spec.StartCommand
		promptMode = "none"
		args = spec.Args
	}

	env := map[string]string{"PARLAY_AGENT_ID": spec.ID}
	if spec.Name != "" {
		env["PARLAY_AGENT_NAME"] = spec.Name
	}
	// BEADS_ACTOR stamps every bead the agent writes through the brain
	// binary (task-svq1q: agents operate on family stores directly). It is
	// derived, never caller-supplied, so an agent cannot impersonate a human
	// or another agent: the parlay.<id> session namespace and the actor
	// namespace agree by construction, and family-wide search can enumerate
	// everything one agent wrote. Non-secret — it rides the template env
	// like every other pair here.
	env["BEADS_ACTOR"] = "parlay-" + spec.ID
	if spec.Name != "" {
		env["PARLAY_AGENT_NAME"] = spec.Name
	}
	if spec.Color != "" {
		env["PARLAY_AGENT_COLOR"] = spec.Color
	}
	if spec.Account != "" {
		env["PARLAY_SPAWN_DEFAULT_ACCOUNT"] = spec.Account
	}
	if spec.Server != "" {
		env["PARLAY_SERVER"] = spec.Server
	}
	if spec.Model != "" {
		env["PARLAY_AGENT_MODEL"] = spec.Model
	}
	envKeys := make([]string, 0, len(env))
	for k := range env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)

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
	// start_command carries the COMPLETE command line, env included: gc's
	// agent-level start_command is the escape-hatch provider resolution
	// (internal/config/resolve.go step 1 at the pin) and it skips
	// mergeAgentOverrides, silently dropping BOTH a separate args field and
	// the agent [env] table (cmd/gc/worker_handle.go sources session env only
	// from the resolved provider's Env, which the escape hatch leaves nil).
	// So the env rides as a /usr/bin/env prefix on the command line itself —
	// the one channel the escape hatch provably delivers. Quoting mirrors
	// gc's internal/shellquote.Join, which the subprocess provider
	// round-trips through `sh -c`. process_names stays the REAL command's
	// basename, never "env".
	cmdline := append(make([]string, 0, len(envKeys)+len(args)+1), start)
	cmdline = append(cmdline, args...)
	envArgs := make([]string, 0, len(envKeys))
	for _, k := range envKeys {
		envArgs = append(envArgs, k+"="+env[k])
	}
	fmt.Fprintf(&b, "start_command = %s\n", tomlString(shellJoin("/usr/bin/env", append(envArgs, cmdline...))))
	fmt.Fprintf(&b, "prompt_mode = %s\n", tomlString(promptMode))
	fmt.Fprintf(&b, "process_names = %s\n", tomlStringArray([]string{filepath.Base(start)}))

	// The [env] table is kept as the structured record of the same pairs: gc
	// drops it today under start_command (see above), and if a future pin
	// starts honouring it the child just receives identical values twice.
	b.WriteString("\n[env]\n")
	for _, k := range envKeys {
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
	// Agent bead-store conventions (task-svq1q: agents operate on family
	// stores directly — the city store holds only runtime/transport beads).
	// Read-wide: `brain search` queries every family store. Write-narrow:
	// your work beads go to the `parlay` family store, addressed explicitly
	// per invocation (never export BEADS_DIR — gc's own bd shell-outs must
	// keep resolving the city-local store):
	//
	//   BEADS_DIR=$HOME/data/parlay/.beads bd create --type task "…"
	//
	// Every write carries your actor stamp automatically (BEADS_ACTOR in
	// your environment); to act on a claimed ticket in another store, name
	// that store explicitly. Never write runtime handles to family stores —
	// no session IDs, ports, PIDs, or herdr panes; reference ticket IDs.
	// Those handles stay city-local, where the sync never reaches.
	p.WriteString("\n## Bead store\n\n")
	p.WriteString("You share the brain family of bead stores with your operator.\n")
	p.WriteString("Read anywhere: `brain search <terms>` queries every store.\n")
	p.WriteString("Write your work beads to the `parlay` store, addressed explicitly:\n")
	p.WriteString("\n    BEADS_DIR=$HOME/data/parlay/.beads bd create --type task \"…\"\n\n")
	p.WriteString("Your writes are stamped with your actor automatically — never override it.\n")
	p.WriteString("Never write runtime handles (session IDs, ports, PIDs, panes) to family\n")
	p.WriteString("stores; reference ticket IDs instead.\n")
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

// shellMetacharacters matches gc's internal/shellquote metacharacter set at
// the pin — an arg containing any of these gets single-quoted.
const shellMetacharacters = " \t\r\n\"'\\|&;$!(){}[]<>?*~#`"

// shellJoin renders command + args as one POSIX shell command line, with the
// same quoting gc's shellquote.Join produces (simple args stay readable, the
// command word is carried verbatim like gc's CommandString does).
func shellJoin(command string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, command)
	for _, arg := range args {
		switch {
		case arg == "":
			parts = append(parts, "''")
		case strings.ContainsAny(arg, shellMetacharacters):
			parts = append(parts, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
		default:
			parts = append(parts, arg)
		}
	}
	return strings.Join(parts, " ")
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
