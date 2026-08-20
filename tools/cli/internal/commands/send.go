package commands

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

type sendResponse struct {
	OK    bool   `json:"ok,omitempty"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error,omitempty"`
}

// registryLookupTimeout bounds the pre-flight target check so a slow or wedged
// server degrades to "skip the check" rather than hanging the send.
const registryLookupTimeout = 5 * time.Second

// Send ports commands.ts's cmdSend. docs/scope-go-cli.md §5 item 1: the
// target agent is parsed from ANY unrecognized --foo token
// (`send --mayor "msg"` -> target "mayor"), which no generic flag parser
// expresses — this hand-rolls the exact loop from commands.ts rather than
// calling internal/args.Parse.
//
// That "any unrecognized --flag is the target" rule is a sharp edge, and it
// drew blood (robots-ngg5). Every OTHER parlay verb that takes an agent spells
// it `--agent <id>` — `parlay listen --agent <id>`, the Monitor line `parlay
// claim` prints — so callers naturally typed
// `parlay send --agent mc-foo --from firstmate "steer"`. Under the old loop
// `--agent` fell through to the catch-all and parsed as target "agent", with
// "mc-foo" folded into the message BODY. The steer landed on a phantom channel
// named `agent` that no relay poll loop watches, the intended recipient never
// saw it, and the caller still got `{ok:true, id:<uuid>}` back. A steer that
// looks delivered and isn't is the worst failure shape there is: the supervisor
// has no signal to retry.
//
// Two changes close that off:
//
//   - `--agent <id>` / `--to <id>` are recognized explicitly and consume the
//     NEXT token as the target, so the house-standard spelling routes correctly
//     instead of silently inventing a channel. The bare `--<agent-id>`
//     shorthand documented in help is unchanged.
//   - The target is checked against the live registry (`GET /api/chat/agents`)
//     before posting. An unregistered target aborts with a non-zero exit and
//     near-match suggestions rather than minting a dead channel. The check
//     fails OPEN — if the registry is unreachable or empty we warn and send
//     anyway, since a transport problem must not become a new way to lose a
//     message. `--force` skips it outright, for deliberately seeding a channel
//     before its agent has registered.
//
// A second pre-flight follows it: refuseStaleWindow (robots-9d2w). Registered
// and live is not the same as WORTH sending to — a pane that finished its task
// and is sitting at its prompt still accepts messages, and continuing it makes
// the new work re-pay for the whole finished session's transcript on every
// turn. See stale.go for the policy; `--force` waives this one too, which is
// the escape hatch for the legitimate case (asking a done agent a follow-up
// question ABOUT the work it just finished — there, the old context is the
// point).
func Send(argv []string) {
	if helpWanted("send", argv) {
		return
	}

	var target, fromOverride string
	var positionals []string
	force := false
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--from":
			i++
			if i < len(argv) {
				fromOverride = argv[i]
			}
		case a == "--agent" || a == "--to":
			// House-standard spelling, shared with `parlay listen --agent`.
			// Must consume the NEXT token — falling through to the catch-all
			// below is exactly what produced the phantom `agent` channel.
			i++
			if i < len(argv) {
				target = argv[i]
			}
		case a == "--force":
			force = true
		case strings.HasPrefix(a, "--"):
			// Any other unrecognized --flag is the target agent id.
			target = a[2:]
		default:
			positionals = append(positionals, a)
		}
	}

	// No args at all -> list targetable agents.
	if target == "" && len(positionals) == 0 {
		agentsList := httpc.GetJSON[[]wire.AgentInfo]("/api/chat/agents")
		if len(agentsList) == 0 {
			fmt.Println("0 agents registered — no one to send to.")
		} else {
			fmt.Printf("%d agent(s) you can message:\n", len(agentsList))
			for _, a := range agentsList {
				fmt.Printf("  send --%s # → %s\n", format.PadEnd(a.ID, 22), a.Name)
			}
		}
		return
	}

	text := strings.TrimSpace(strings.Join(positionals, " "))
	if text == "" {
		t := target
		if t == "" {
			t = "<agent-id>"
		}
		httpc.Die(fmt.Sprintf("parlay send: message text required (e.g. send --%s \"your message\")", t), config.ExitUsage)
		return
	}
	if target == "" {
		httpc.Die(`parlay send: no target agent — use send --<agent-id> "msg" or bare send to list agents`, config.ExitUsage)
		return
	}

	if !force {
		requireRegisteredTarget(target)
		refuseStaleWindow(target)
	}

	from := strings.TrimSpace(fromOverride)
	if from == "" {
		from = strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID"))
	}

	body := map[string]any{"text": text, "toAgent": target}
	if from != "" {
		body["from"] = from
	}

	r := httpc.PostJSON[sendResponse]("/api/chat/send", body)
	if r.Error != "" {
		httpc.Die(fmt.Sprintf("send failed: %s", r.Error), config.ExitRuntime)
		return
	}
	fromSuffix := ""
	if from != "" {
		fromSuffix = fmt.Sprintf(" (from %s)", from)
	}
	fmt.Printf("sent to %s%s — id %s\n", target, fromSuffix, r.ID)
	format.NextStep("parlay history 5")
}

// requireRegisteredTarget aborts the send when target is not a registered
// agent channel. See Send's doc comment for why: POST /api/chat/send accepts
// any string as toAgent and mints a message id for it, so a send to an
// unregistered channel is a message written where nothing is polling —
// indistinguishable, from the caller's side, from a delivered one.
//
// Deliberately fails OPEN. If the registry cannot be read (server down, route
// missing, empty list) we warn on stderr and let the send proceed: this check
// exists to catch typos and misparsed flags, and turning every registry
// hiccup into a refused send would trade one lost-message mode for another.
func requireRegisteredTarget(target string) {
	agentsList, ok := httpc.TryGetJSON[[]wire.AgentInfo]("/api/chat/agents", registryLookupTimeout)
	if !ok || len(agentsList) == 0 {
		fmt.Fprintf(os.Stderr,
			"parlay send: could not read the agent registry — sending to %q unverified.\n", target)
		return
	}

	ids := make([]string, 0, len(agentsList))
	for _, a := range agentsList {
		if a.ID == target {
			return
		}
		ids = append(ids, a.ID)
	}

	msg := fmt.Sprintf("parlay send: %q is not a registered agent — refusing to send.\n", target) +
		"  Nothing polls an unregistered channel, so this message would be accepted and never delivered.\n"
	if near := nearestAgentIDs(target, ids); len(near) > 0 {
		msg += "  Did you mean: " + strings.Join(near, ", ") + "\n"
	}
	msg += "  Run 'parlay send' with no arguments to list every targetable agent.\n" +
		"  Use --force to send anyway (e.g. seeding a channel before its agent registers)."
	httpc.Die(msg, config.ExitUsage)
}

// refuseStaleWindow aborts the send when target is a spent pane — one that
// posted a terminal status and is sitting at its prompt (robots-9d2w).
//
// requireRegisteredTarget above answers "will this message be delivered?".
// This answers the question after it: "should it be?". A finished agent is
// still registered, still enrolled, and still accepts messages, so a re-task
// lands in a session whose transcript is entirely the job it already closed —
// and every turn of the new work re-pays for it. The remedy is to relaunch,
// not to continue, so the refusal prints the relaunch commands rather than
// just saying no.
//
// Fails OPEN in exactly the same way and for the same reason: unknown state
// (relay down, nothing recorded) is not stale, so a transport problem can
// never become a refused send. Policy lives in ClassifyStaleWindow — this is
// only the send-side reaction to it.
func refuseStaleWindow(target string) {
	v := ClassifyStaleWindow(resolveStaleWindow(target))
	if !v.Stale {
		return
	}
	httpc.Die(
		fmt.Sprintf("parlay send: %q is a STALE WINDOW (%s) — refusing to send.\n", target, v.Reason)+
			"  That pane already finished its work; continuing it makes the new task re-pay for the\n"+
			"  whole finished session on every turn (this is what the harness means by \"/clear to save …\").\n"+
			relaunchAdvice(target)+"\n"+
			"  Use --force to send anyway (e.g. a follow-up question ABOUT the work it just finished).",
		config.ExitUsage,
	)
}

// nearestAgentIDs returns up to 5 registered ids sharing a substring with
// target, case-insensitively — best-effort, enough to turn "refused" into
// "here is the id you meant" for the common typo and copy-paste cases.
func nearestAgentIDs(target string, ids []string) []string {
	t := strings.ToLower(target)
	var hits []string
	for _, id := range ids {
		l := strings.ToLower(id)
		if strings.Contains(l, t) || strings.Contains(t, l) {
			hits = append(hits, id)
		}
	}
	sort.Strings(hits)
	if len(hits) > 5 {
		hits = hits[:5]
	}
	return hits
}
