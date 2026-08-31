// Status-lift unit 7: `parlay status-migrate` — the one-shot tool that
// brings a pre-lift agents root onto the bead/event pipeline by REPLAYING
// each agent's existing status lines into its per-agent event log and
// through the same ApplyStatus fold the live writer uses. Replay, never
// truncate: the original status file is not modified at all (a full backup
// copy is written besides, belt and braces), so every legacy consumer keeps
// working and the migration is trivially abortable.
//
// RUNNING THIS AGAINST THE LIVE ~/.parlay/agents IS CAPTAIN-GATED
// (robots-lor, standing security constraint). The tool enforces the gate
// structurally: --agents-root is required with no default, and the canonical
// live root is refused unless --live is also passed. Everything else —
// implementation, tests, shakedown — happens against fixture copies.
//
// Consumer cursors are seeded at head: after replay, .supervise-seq is set
// to the log's LatestSeq so supervise does not re-fire history the captain
// has already seen. The migration deliberately attaches no gc_session
// pointer — replayed history predates the spawn seam's record; the next live
// dual-write attaches it (crewDualWrite merges the stamp on every write).
package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/crewevents"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/parlaybeads"
)

// migrateBackupName is the full-copy backup written next to each migrated
// status file. Its existence is also the idempotence latch: a second --apply
// run refuses rather than silently overwriting the first run's backup.
const migrateBackupName = "status.pre-migrate.bak"

// statusMigrateTimeout bounds the whole apply pass (store open + every
// ApplyStatus fold), not one write — a root can hold hundreds of agents.
const statusMigrateTimeout = 10 * time.Minute

// canonicalLiveAgentsRoot is the captain's production agents root, the one
// path this tool refuses without --live (robots-lor).
func canonicalLiveAgentsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".parlay", "agents")
}

func isLiveAgentsRoot(root string) bool {
	live := canonicalLiveAgentsRoot()
	if live == "" {
		return false
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	return filepath.Clean(abs) == filepath.Clean(live)
}

// migrateAgentPlan is one agent's share of the dry-run report and the apply
// work list.
type migrateAgentPlan struct {
	agentID     string
	dir         string
	statusFile  string
	fileBytes   []byte
	lines       []parsedStatus
	unparseable int
	skip        string // non-empty = skipped, with reason
}

// planStatusMigration walks root and decides, per agent directory, whether
// there is anything to replay and whether it is safe to. Pure planning — no
// writes.
func planStatusMigration(root, only string) ([]migrateAgentPlan, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var plans []migrateAgentPlan
	for _, agent := range names {
		if only != "" && agent != only {
			continue
		}
		dir := filepath.Join(root, agent)
		p := migrateAgentPlan{agentID: agent, dir: dir, statusFile: filepath.Join(dir, "status")}

		if _, err := os.Stat(crewevents.File(dir)); err == nil {
			// Dual-write already began here: the log already holds these
			// transitions (or will), and a replay would duplicate every one.
			p.skip = "events.jsonl already exists (dual-write already began) — refusing to replay"
			plans = append(plans, p)
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, migrateBackupName)); err == nil {
			p.skip = migrateBackupName + " already exists — this agent was already migrated (or a prior run was interrupted; inspect before retrying)"
			plans = append(plans, p)
			continue
		}
		data, err := os.ReadFile(p.statusFile)
		if err != nil {
			if os.IsNotExist(err) {
				continue // nothing to migrate, not worth a report line
			}
			p.skip = fmt.Sprintf("status file unreadable: %v", err)
			plans = append(plans, p)
			continue
		}
		p.fileBytes = data
		for _, line := range nonEmptyLines(string(data)) {
			if parsed, ok := parseStatusLine(line); ok {
				p.lines = append(p.lines, parsed)
			} else {
				p.unparseable++
			}
		}
		if len(p.lines) == 0 {
			p.skip = fmt.Sprintf("no parseable status lines (%d unparseable kept in place)", p.unparseable)
		}
		plans = append(plans, p)
	}
	return plans, nil
}

// applyAgentMigration performs one agent's replay: backup copy, event
// appends, ApplyStatus folds, cursor seed. Any error aborts the whole run —
// this tool is captain-run and loud; partial state is inspectable (events
// are append-only, the original file untouched).
func applyAgentMigration(ctx context.Context, c parlaybeads.Client, p migrateAgentPlan, at string) (uint64, error) {
	backup := filepath.Join(p.dir, migrateBackupName)
	// O_EXCL: the plan already refused an existing backup; this closes the
	// race and guarantees a re-run cannot clobber the first backup.
	bf, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("backup: %w", err)
	}
	if _, err := bf.Write(p.fileBytes); err != nil {
		bf.Close()
		return 0, fmt.Errorf("backup: %w", err)
	}
	if err := bf.Close(); err != nil {
		return 0, fmt.Errorf("backup: %w", err)
	}

	evFile := crewevents.File(p.dir)
	var head uint64
	for _, line := range p.lines {
		ev, err := crewevents.Append(evFile, crewevents.Event{
			At:    at,
			Name:  crewevents.EventCrewStatus,
			Agent: p.agentID,
			Verb:  line.verb,
			Key:   line.key,
			Note:  line.note,
		})
		if err != nil {
			return 0, fmt.Errorf("event append: %w", err)
		}
		head = ev.Seq
		st := parlaybeads.CrewStatus{AgentID: p.agentID, Verb: line.verb, Key: line.key, Note: line.note, At: at}
		if _, err := parlaybeads.ApplyStatus(ctx, c, st, nil); err != nil {
			return 0, fmt.Errorf("bead fold (after %d event(s) appended): %w", head, err)
		}
	}

	// Seed the supervise cursor at head: history is not news. Append-shaped
	// like writeSeenSeq (last line wins), but against THIS root, not the
	// process's own identity root.
	f, err := os.OpenFile(filepath.Join(p.dir, ".supervise-seq"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("cursor seed: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", head); err != nil {
		f.Close()
		return 0, fmt.Errorf("cursor seed: %w", err)
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("cursor seed: %w", err)
	}
	return head, nil
}

// StatusMigrate is the unit-7 verb.
func StatusMigrate(argv []string) {
	if helpWanted("status-migrate", argv) {
		return
	}
	r := args.Parse("status-migrate", argv, []string{"--apply", "--live"}, []string{"--agents-root", "--agent"})

	root, _ := r.String("--agents-root")
	root = strings.TrimSpace(root)
	if root == "" {
		httpc.Die("parlay status-migrate: --agents-root is required (no default on purpose — say exactly which tree you are migrating)", config.ExitUsage)
		return
	}
	if isLiveAgentsRoot(root) && !r.Bool("--live") {
		httpc.Die("parlay status-migrate: refusing the live agents root without --live — migrating the captain's production install is captain-gated (robots-lor). Test against a fixture copy instead.", config.ExitUsage)
		return
	}
	store := crewStoreDir()
	if store == "" {
		httpc.Die("parlay status-migrate: PARLAY_CREW_STORE must name the target bead store (the same gate the dual-writer uses)", config.ExitUsage)
		return
	}
	only, _ := r.String("--agent")
	only = strings.TrimSpace(only)

	plans, err := planStatusMigration(root, only)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay status-migrate: %v", err), config.ExitRuntime)
		return
	}

	todo := 0
	for _, p := range plans {
		if p.skip != "" {
			fmt.Printf("%s: SKIP — %s\n", p.agentID, p.skip)
			continue
		}
		todo++
		if p.unparseable > 0 {
			fmt.Printf("%s: %d line(s) to replay (%d unparseable kept in place, not replayed)\n", p.agentID, len(p.lines), p.unparseable)
		} else {
			fmt.Printf("%s: %d line(s) to replay\n", p.agentID, len(p.lines))
		}
	}
	if todo == 0 {
		fmt.Println("status-migrate: nothing to do")
		return
	}
	if !r.Bool("--apply") {
		fmt.Printf("dry run — nothing changed. Re-run with --apply to migrate %d agent(s).\n", todo)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), statusMigrateTimeout)
	defer cancel()
	c, err := crewStoreOpen(ctx, store, "status-migrate")
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay status-migrate: opening crew store: %v", err), config.ExitRuntime)
		return
	}
	defer c.Close()

	at := time.Now().UTC().Format(time.RFC3339)
	migrated := 0
	for _, p := range plans {
		if p.skip != "" {
			continue
		}
		head, err := applyAgentMigration(ctx, c, p, at)
		if err != nil {
			httpc.Die(fmt.Sprintf("parlay status-migrate: %s: %v — run aborted; original status files are untouched, appended events are inspectable in %s", p.agentID, err, crewevents.File(p.dir)), config.ExitRuntime)
			return
		}
		fmt.Printf("%s: replayed %d line(s); backup %s; supervise cursor seeded at %d\n", p.agentID, len(p.lines), filepath.Join(p.dir, migrateBackupName), head)
		migrated++
	}
	fmt.Printf("migrated %d agent(s). Originals untouched; enable dual-write (PARLAY_CREW_STORE) before flipping readers (PARLAY_CREW_READ_BEADS=1).\n", migrated)
}
