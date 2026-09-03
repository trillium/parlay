package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
)

var workspaceIDRe = regexp.MustCompile(`^w[A-Za-z0-9]+$`)

// resolveWorkspace mirrors bash's resolve_workspace() (lines 1133-1164):
// pass a raw workspace ID through unchanged; otherwise treat want as a label,
// resolving it against `herdr workspace list` and creating one via
// `herdr workspace create` if none matches. Shells directly to the herdr
// binary rather than going through the Launcher interface — bash's own
// resolve_workspace does the same (a free function, not part of any
// herdr-wrapper abstraction), and this runs before a Launcher is even needed
// when --workspace is the only herdr interaction a caller wants (e.g. list
// mode never reaches this).
func resolveWorkspace(want string) (string, error) {
	if workspaceIDRe.MatchString(want) {
		return want, nil
	}

	listOut, err := runHerdr("workspace", "list")
	if err != nil {
		return "", fmt.Errorf("herdr workspace list failed: %w", err)
	}
	var listResp struct {
		Result struct {
			Workspaces []struct {
				WorkspaceID string `json:"workspace_id"`
				Label       string `json:"label"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	_ = json.Unmarshal(listOut, &listResp)
	for _, w := range listResp.Result.Workspaces {
		if w.Label == want {
			fmt.Fprintf(os.Stderr, "parlay-spawn: workspace '%s' resolved to %s\n", want, w.WorkspaceID)
			return w.WorkspaceID, nil
		}
	}

	fmt.Fprintf(os.Stderr, "parlay-spawn: workspace '%s' not found, creating...\n", want)
	createOut, err := runHerdr("workspace", "create", "--label", want, "--no-focus")
	if err != nil {
		return "", fmt.Errorf("herdr workspace create '%s' failed: %w", want, err)
	}
	var createResp struct {
		Result struct {
			Workspace struct {
				WorkspaceID string `json:"workspace_id"`
			} `json:"workspace"`
		} `json:"result"`
	}
	if err := json.Unmarshal(createOut, &createResp); err != nil || createResp.Result.Workspace.WorkspaceID == "" {
		return "", fmt.Errorf("could not parse workspace_id from create response")
	}
	fmt.Fprintf(os.Stderr, "parlay-spawn: created workspace '%s' -> %s\n", want, createResp.Result.Workspace.WorkspaceID)
	return createResp.Result.Workspace.WorkspaceID, nil
}

// runHerdr shells to the herdr binary and returns stdout, matching bash's
// `herdr ... 2>/dev/null` capture-stdout-only convention throughout this
// file's herdr calls.
func runHerdr(args ...string) ([]byte, error) {
	var out bytes.Buffer
	cmd := exec.Command("herdr", args...)
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
