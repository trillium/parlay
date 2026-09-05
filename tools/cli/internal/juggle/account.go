// Package account is a Go port of ccjuggler.py — the source of truth for
// resolving and storing per-account Claude Code OAuth tokens. Keep this
// package's behavior identical to ccjuggler.py; consumers (the juggle CLI,
// parlay) should depend on this package rather than reimplementing lookup.
package account

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const KeychainAccountDefault = "ccjuggler"

type Account struct {
	Name              string `json:"name"`
	Email             string `json:"email,omitempty"`
	Tier              string `json:"tier,omitempty"`
	SubscriptionStart string `json:"subscription_start,omitempty"`
	SubscriptionEnd   string `json:"subscription_end,omitempty"`
	KeychainService   string `json:"keychain_service,omitempty"`
	KeychainAccount   string `json:"keychain_account,omitempty"`
	TokenFormat       string `json:"token_format,omitempty"`
}

type accountsFile struct {
	Accounts []Account `json:"accounts"`
}

// AccountsFilePath mirrors ccjuggler.py's ACCOUNTS_FILE: overridable via
// CCJUGGLER_ACCOUNTS_FILE (used by tests), else the real default location.
func AccountsFilePath() string {
	if v := os.Getenv("CCJUGGLER_ACCOUNTS_FILE"); v != "" {
		return v
	}
	return filepath.Join(os.Getenv("HOME"), "code", "juggle", "accounts.json")
}

// StateFilePath mirrors ccjuggler.py's STATE_FILE (mode_indicator_state.json,
// written by poll_usage.py — this package only ever reads it).
func StateFilePath() string {
	if v := os.Getenv("CCJUGGLER_STATE_FILE"); v != "" {
		return v
	}
	return filepath.Join(os.Getenv("HOME"), "code", "juggle", "mode_indicator_state.json")
}

// LoadAccounts mirrors load_accounts(): missing or unparseable file -> empty list, no error.
func LoadAccounts() []Account {
	data, err := os.ReadFile(AccountsFilePath())
	if err != nil {
		return nil
	}
	var f accountsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil
	}
	return f.Accounts
}

func SaveAccounts(accounts []Account) error {
	data, err := json.MarshalIndent(accountsFile{Accounts: accounts}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(AccountsFilePath(), data, 0644)
}

func FindAccount(accounts []Account, name string) (Account, bool) {
	for _, a := range accounts {
		if a.Name == name {
			return a, true
		}
	}
	return Account{}, false
}

type credsFile struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}

// GetToken mirrors get_token(): reads the account's Keychain entry, then
// unwraps it per TokenFormat ("raw" default, or "claude-credentials-json").
func GetToken(a Account) (string, error) {
	args := []string{"find-generic-password", "-s", a.KeychainService, "-w"}
	if a.KeychainAccount != "" {
		args = append(args, "-a", a.KeychainAccount)
	}
	out, err := exec.Command("security", args...).Output()
	if err != nil {
		return "", err
	}
	raw := strings.TrimSpace(string(out))
	if a.TokenFormat == "claude-credentials-json" {
		var creds credsFile
		if err := json.Unmarshal([]byte(raw), &creds); err != nil {
			return "", fmt.Errorf("parsing claude-credentials-json: %w", err)
		}
		return creds.ClaudeAiOauth.AccessToken, nil
	}
	return raw, nil
}

func StoreToken(service, token string) error {
	return exec.Command("security", "add-generic-password",
		"-s", service, "-a", KeychainAccountDefault, "-w", token, "-U").Run()
}

func DeleteToken(service string) {
	_ = exec.Command("security", "delete-generic-password",
		"-s", service, "-a", KeychainAccountDefault).Run()
}

// State mirrors mode_indicator_state.json's shape. Snapshots are kept as
// loosely-typed maps (like Python's dict.get) since fields are optional and
// the Python code distinguishes "missing" from "zero".
type State struct {
	Accounts map[string]map[string]any `json:"accounts"`
}

func LoadState() State {
	data, err := os.ReadFile(StateFilePath())
	if err != nil {
		return State{}
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}
	}
	return s
}

func getOr(m map[string]any, key string, def any) any {
	if v, ok := m[key]; ok {
		return v
	}
	return def
}

// UsageSummary mirrors account_usage_summary().
func UsageSummary(name string, s State) string {
	snap, ok := s.Accounts[name]
	if !ok {
		return "no data yet"
	}
	fiveH := getOr(snap, "five_hour_percent", "?")
	week := getOr(snap, "week_percent", "?")
	weekRem, _ := getOr(snap, "week_remaining", "").(string)
	sub := getOr(snap, "subscription_status", "?")
	tierLabel, _ := getOr(snap, "rate_limit_tier", "").(string)
	updated, _ := getOr(snap, "last_updated", "").(string)
	if len(updated) > 16 {
		updated = updated[:16]
	}
	updated = strings.ReplaceAll(updated, "T", " ")

	parts := []string{fmt.Sprintf("5h=%v%%  7d=%v%%", fiveH, week)}
	if weekRem != "" {
		parts = append(parts, fmt.Sprintf("resets %s", weekRem))
	}
	parts = append(parts, fmt.Sprintf("[%v]", sub))
	if tierLabel != "" {
		parts = append(parts, tierLabel)
	}
	if updated != "" {
		parts = append(parts, fmt.Sprintf("@ %s", updated))
	}
	return strings.Join(parts, "  ")
}

// BestAccount mirrors best_account(): lowest 5h usage, tie-break on week usage.
func BestAccount(accounts []Account, s State) (Account, bool) {
	if len(accounts) == 0 {
		return Account{}, false
	}
	type scored struct {
		fiveH, week float64
		acct        Account
	}
	scoredList := make([]scored, 0, len(accounts))
	for _, a := range accounts {
		fiveH, week := 100.0, 100.0
		if snap, ok := s.Accounts[a.Name]; ok {
			if v, ok := getOr(snap, "five_hour_percent", nil).(float64); ok {
				fiveH = v
			}
			if v, ok := getOr(snap, "week_percent", nil).(float64); ok {
				week = v
			}
		}
		scoredList = append(scoredList, scored{fiveH, week, a})
	}
	sort.SliceStable(scoredList, func(i, j int) bool {
		if scoredList[i].fiveH != scoredList[j].fiveH {
			return scoredList[i].fiveH < scoredList[j].fiveH
		}
		return scoredList[i].week < scoredList[j].week
	})
	return scoredList[0].acct, true
}
