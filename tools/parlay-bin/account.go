package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolveAccountToken resolves a CLAUDE_CODE_OAUTH_TOKEN for a ccjuggler
// account name by delegating to the canonical ccjuggler engine:
// `python3 ~/code/juggle/ccjuggler.py use <account>`, whose single stdout
// line is `export CLAUDE_CODE_OAUTH_TOKEN=<token>`. Mirrors
// packages/ccjuggler's resolveToken.
func resolveAccountToken(account string) (string, error) {
	ccjuggler := filepath.Join(os.Getenv("HOME"), "code", "juggle", "ccjuggler.py")
	out, err := exec.Command("python3", ccjuggler, "use", account).Output()
	if err != nil {
		return "", fmt.Errorf("--account %q: ccjuggler subprocess failed: %w", account, err)
	}

	for _, line := range strings.Split(string(out), "\n") {
		if token, ok := strings.CutPrefix(strings.TrimSpace(line), "export CLAUDE_CODE_OAUTH_TOKEN="); ok {
			if token != "" {
				return token, nil
			}
		}
	}

	return "", fmt.Errorf("--account %q: no token found — python3 %s use %s did not emit a token line", account, ccjuggler, account)
}
