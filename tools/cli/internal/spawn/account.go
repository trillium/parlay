package spawn

import (
	"fmt"

	account "github.com/trillium/parlay/tools/cli/internal/juggle"
)

// resolveAccountToken resolves a CLAUDE_CODE_OAUTH_TOKEN for a ccjuggler
// account name by delegating to the canonical juggle account package (the Go
// port of ccjuggler.py), which handles both token_format=raw and
// claude-credentials-json. Mirrors packages/ccjuggler's resolveToken.
func resolveAccountToken(accountName string) (string, error) {
	accounts := account.LoadAccounts()
	if len(accounts) == 0 {
		return "", fmt.Errorf("--account %q: no ccjuggler accounts found", accountName)
	}
	a, ok := account.FindAccount(accounts, accountName)
	if !ok {
		return "", fmt.Errorf("--account %q: account not found in accounts.json", accountName)
	}
	token, err := account.GetToken(a)
	if err != nil {
		return "", fmt.Errorf("--account %q: resolving token: %w", accountName, err)
	}
	if token == "" {
		return "", fmt.Errorf("--account %q: resolved empty token", accountName)
	}
	return token, nil
}
