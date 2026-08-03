package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolveAccountToken resolves a CLAUDE_CODE_OAUTH_TOKEN for a ccjuggler
// account name: macOS keychain first, then a flat-file fallback. Mirrors
// bin/parlay-spawn's resolve_account_token() (lines 62–76).
func resolveAccountToken(account string) (string, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-a", "ccjuggler-"+account, "-s", "ccjuggler", "-w").Output()
	if err == nil {
		if token := strings.TrimSpace(string(out)); token != "" {
			return token, nil
		}
	}

	path := filepath.Join(os.Getenv("HOME"), ".ccjuggler", account, ".oauth-token")
	if data, readErr := os.ReadFile(path); readErr == nil {
		return strings.TrimSpace(string(data)), nil
	}

	return "", fmt.Errorf("--account %q: no token found — tried keychain 'ccjuggler-%s' and %s", account, account, path)
}
