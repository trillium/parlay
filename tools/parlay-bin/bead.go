package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// beadsRequiredErrorTemplate mirrors bash's template resolution (lines
// 166-176): a user override under $PARLAY_STATE_HOME/templates/ wins over the
// repo copy at share/parlay/beads-required-error.txt. Returns "" (and the
// caller falls back to its own generic message) if neither exists — the same
// degrade bash's BEADS_REQUIRED_ERROR_TMPL="" fallback performs.
func beadsRequiredErrorTemplate() string {
	stateHome := os.Getenv("PARLAY_STATE_HOME")
	if stateHome == "" {
		stateHome = filepath.Join(os.Getenv("HOME"), ".parlay")
	}
	userPath := filepath.Join(stateHome, "templates", "beads-required-error.txt")
	if data, err := os.ReadFile(userPath); err == nil {
		return string(data)
	}

	const rel = "share/parlay/beads-required-error.txt"
	if p, ok := findUpward(os.Getenv("PWD"), rel); ok {
		if data, err := os.ReadFile(p); err == nil {
			return string(data)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if p, ok := findUpward(cwd, rel); ok {
			if data, err := os.ReadFile(p); err == nil {
				return string(data)
			}
		}
	}
	return ""
}

// beadGateError is returned by beadGate to distinguish exit-2 refusals
// (beads-required with no --bead) from exit-1 refusals (a named bead that is
// closed or unresolvable) — spawn.go maps this back to the matching process
// exit code, mirroring bash's two distinct `exit` statuses (lines 742-814).
type beadGateError struct {
	msg      string
	exitCode int
}

func (e *beadGateError) Error() string { return e.msg }

// beadGate mirrors bash's bead_gate() (lines 728-814): enforced BEFORE any
// registration/tab/subprocess side effect exists.
//  1. beadsRequired && beadID == "" → refuse (exit 2)
//  2. beadID names a bead that is closed or that no store CLI can resolve →
//     refuse (exit 1)
//
// force (from --force) downgrades beadsRequired to off for this call, same
// as bash's `[ "$FORCE" -eq 1 ] && BEADS_REQUIRED=0`.
func beadGate(beadID string, beadsRequired, force bool) error {
	if force {
		beadsRequired = false
	}

	if beadID == "" {
		if beadsRequired {
			if tmpl := beadsRequiredErrorTemplate(); tmpl != "" {
				return &beadGateError{msg: strings.TrimRight(tmpl, "\n"), exitCode: 2}
			}
			return &beadGateError{
				msg: "parlay-spawn: beads-required mode is ON — every spawn must name an OPEN beads work item.\n" +
					"  Pass --bead <id>. The bead's lifecycle governs the agent.",
				exitCode: 2,
			}
		}
		return nil
	}

	if os.Getenv("PARLAY_SPAWN_SKIP_BEAD_CHECK") != "" {
		fmt.Fprintf(os.Stderr, "parlay-spawn: PARLAY_SPAWN_SKIP_BEAD_CHECK set — NOT verifying that %s is open.\n", beadID)
		return nil
	}

	status, resolvable := resolveBeadStatus(beadID)
	if !resolvable {
		fmt.Fprintf(os.Stderr, "parlay-spawn: WARNING — no store CLI on PATH for %s; cannot verify the bead is open. Proceeding with the binding recorded.\n", beadID)
		return nil
	}

	switch strings.ToLower(status) {
	case "closed", "done", "completed", "resolved":
		return &beadGateError{
			msg: fmt.Sprintf("parlay-spawn: bead %s is %s — refusing to spawn.\n"+
				"  A closed bead means the work is over: the agent would be registered, launch,\n"+
				"  and be refused its first relaunch. Re-open the bead, or spawn against an open one.", beadID, status),
			exitCode: 1,
		}
	case "":
		return &beadGateError{
			msg: fmt.Sprintf("parlay-spawn: could not read a status for %s.\n"+
				"  Refusing to spawn: a bead this script cannot resolve cannot govern the agent's\n"+
				"  lifecycle, and a typo'd id would bind the agent to nothing. Check the id, or set\n"+
				"  PARLAY_SPAWN_SKIP_BEAD_CHECK=1 to spawn without verifying.", beadID),
			exitCode: 1,
		}
	}

	fmt.Fprintf(os.Stderr, "parlay-spawn: bead %s is %s — binding it to this agent.\n", beadID, status)
	return nil
}

// resolveBeadStatus mirrors bash's store-CLI resolution + status read (lines
// 770-796): the bead id's leading token before its first '-' (task-oyaj →
// task) is tried first, falling back to a bare `bd`. resolvable=false means
// no CLI at all could be found — the caller treats that as "proceed
// unverified", not as a refusal.
func resolveBeadStatus(beadID string) (status string, resolvable bool) {
	store, _, found := strings.Cut(beadID, "-")
	bin := ""
	if found && store != "" {
		if _, err := exec.LookPath(store); err == nil {
			bin = store
		}
	}
	if bin == "" {
		if _, err := exec.LookPath("bd"); err == nil {
			bin = "bd"
		}
	}
	if bin == "" {
		return "", false
	}

	var out bytes.Buffer
	cmd := exec.Command(bin, "show", beadID, "--json")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", true
	}
	return extractBeadStatus(out.Bytes(), beadID), true
}

// extractBeadStatus mirrors bash's jq fallback-to-sed extraction (lines
// 788-796): matches the bead by id anywhere in the document (an array, a bare
// object, or a wrapped envelope all work), returning its status field.
func extractBeadStatus(jsonBytes []byte, beadID string) string {
	if len(jsonBytes) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(jsonBytes, &v); err != nil {
		return ""
	}
	if status, ok := findBeadStatusByID(v, beadID); ok {
		return status
	}
	return ""
}

func findBeadStatusByID(v any, beadID string) (string, bool) {
	switch t := v.(type) {
	case map[string]any:
		if id, ok := t["id"].(string); ok && id == beadID {
			if status, ok := t["status"].(string); ok {
				return status, true
			}
		}
		for _, val := range t {
			if status, ok := findBeadStatusByID(val, beadID); ok {
				return status, true
			}
		}
	case []any:
		for _, item := range t {
			if status, ok := findBeadStatusByID(item, beadID); ok {
				return status, true
			}
		}
	}
	return "", false
}
