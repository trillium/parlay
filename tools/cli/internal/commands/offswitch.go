package commands

import (
	"fmt"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// ── the off switch, in its direct CLI form ─────────────────────────────────────

// OffSwitch backs both `parlay off` and `parlay on`. With no positional, or
// with `status`, it reports what is off instead — asking for nothing must never
// silently mean "turn nothing off" and print nothing.
func OffSwitch(argv []string, off bool, verb string) {
	if helpWanted(verb, argv) {
		return
	}
	r := args.Parse(verb, argv, []string{"--status"}, []string{"--by", "--surface"})
	if r.Bool("--status") || len(r.Positionals) == 0 || r.Positionals[0] == "status" {
		offStatus(verb)
		return
	}
	if len(r.Positionals) != 2 {
		httpc.Die(fmt.Sprintf(
			"parlay %s: want <connection|action> <id>, or `parlay %s status` (got %s)",
			verb, verb, strings.Join(r.Positionals, " ")), config.ExitUsage)
		return
	}
	kind, id, ok := splitOffTarget(r.Positionals[0] + ":" + r.Positionals[1])
	if !ok {
		httpc.Die(fmt.Sprintf("parlay %s: kind must be connection or action (got %q)", verb, r.Positionals[0]), config.ExitUsage)
		return
	}
	by, _ := r.String("--by")
	surface, _ := r.String("--surface")
	if surface == "" {
		surface = "cli"
	}
	setOffSwitch(kind, id, off, by, surface, false)
}

// setOffSwitch performs the flip and ALWAYS re-reads the server's answer rather
// than assuming success: the whole point of the off switch is that it takes
// effect, and a printout claiming a state the server did not accept would be
// worse than no printout.
//
// `quiet` suppresses the human confirmation while keeping the flip and the
// server's verdict. It exists for callers whose stdout must remain ONE machine-
// readable document (`parlay action-log --json --off …`), where a text line in
// front of the JSON makes the output undecodable even though the change landed.
func setOffSwitch(kind, id string, off bool, by, surface string, quiet bool) []offEntry {
	body := map[string]any{"kind": kind, "id": id, "off": off, "surface": surface}
	if by != "" {
		body["by"] = by
	}
	resp := httpc.PostJSON[struct {
		OK      bool       `json:"ok"`
		Kind    string     `json:"kind"`
		ID      string     `json:"id"`
		Off     bool       `json:"off"`
		Changed bool       `json:"changed"`
		Targets []offEntry `json:"targets"`
		Error   string     `json:"error"`
	}]("/api/chat/off-switch", body)
	if resp.Error != "" {
		httpc.Die("off switch refused: "+resp.Error, config.ExitRuntime)
		return nil
	}
	if !quiet {
		state := "ON"
		if resp.Off {
			state = "OFF"
		}
		change := "already"
		if resp.Changed {
			change = "changed"
		}
		fmt.Printf("%s %s: %s (%s)\n", resp.Kind, resp.ID, state, change)
		if resp.Off {
			fmt.Printf("  effect: %s\n", offEffect(resp.Kind))
		}
		printOffLine(resp.Targets)
	}
	return resp.Targets
}

// offEffect states what the flip actually does, so an operator never has to
// read the source to know whether "off" means "hidden" or "revoked".
func offEffect(kind string) string {
	if kind == "connection" {
		return "this device's evaluations are refused before the engine is called, and its pending fires are refused too"
	}
	return "this command's emissions are suppressed; every other command is unaffected"
}

// offStatus prints what is off right now.
func offStatus(verb string) {
	resp := httpc.GetJSON[struct {
		OK      bool       `json:"ok"`
		Targets []offEntry `json:"targets"`
	}]("/api/chat/off-switch")
	fmt.Printf("parlay %s: %d target(s) off\n", verb, len(resp.Targets))
	printOffLine(resp.Targets)
	if len(resp.Targets) == 0 {
		fmt.Println("turn one off from anywhere: `parlay off connection <device-id>`")
		fmt.Println("                            `parlay off action <command-id>`")
		fmt.Println("or from the logs you are reading: `parlay action-log --off action:<command-id>`")
	}
}

// splitOffTarget parses the `<kind>:<id>` form shared by `--off` and the
// positional form of `parlay off <kind> <id>`.
func splitOffTarget(raw string) (kind, id string, ok bool) {
	kind, id, found := strings.Cut(raw, ":")
	if !found || id == "" {
		return "", "", false
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "connection" && kind != "action" {
		return "", "", false
	}
	return kind, strings.TrimSpace(id), true
}
