// Package config is the parlay CLI's shared config: server URL resolution,
// persisted config file, and process exit codes.
//
// Ported from packages/cli/src/config.ts — see docs/scope-go-cli.md §5 item 5
// for why the resolution precedence and env var names must stay exact
// (PARLAY_SERVER, PARLAY_STATE_HOME; asserted by `parlay doctor` and this
// repo's CLAUDE.md).
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DefaultServer is the coded fallback when neither PARLAY_SERVER nor a
// persisted config value is set.
const DefaultServer = "http://localhost:4242"

// Exit codes: 0 = ok, 1 = runtime/server error, 2 = usage error (bad flag/command/args).
const (
	ExitOK      = 0
	ExitRuntime = 1
	ExitUsage   = 2
)

// TruncateAt is the default line-truncation width used by internal/format.
const TruncateAt = 100

// StateHome is the root directory for all persisted parlay state. Same
// override convention as commands-guard.ts / robots-watch's cursor.ts:
// $PARLAY_STATE_HOME (default ~/.parlay). Tests should set this env var to a
// tmp dir so a persisted config on the machine running them is never read.
func StateHome() string {
	if h := os.Getenv("PARLAY_STATE_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".parlay")
}

func configPath() string {
	return filepath.Join(StateHome(), "config.json")
}

type persistedConfig struct {
	Server string `json:"server,omitempty"`
}

// A missing or corrupt config file is treated as empty — resolution falls
// through to the next precedence level, matching config.ts's try/catch.
func readPersistedConfig() persistedConfig {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return persistedConfig{}
	}
	var cfg persistedConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return persistedConfig{}
	}
	return cfg
}

func writePersistedConfig(cfg persistedConfig) error {
	dir := StateHome()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	encErr := enc.Encode(cfg)
	// Sync before Close, and before the rename: a rename that lands ahead of
	// the data publishes a correctly-named config file holding nothing, which
	// is the exact failure an "atomic swap" is supposed to rule out. Only
	// attempted when the encode succeeded — there is nothing worth flushing
	// otherwise, and the encode error is the one the caller needs.
	var syncErr error
	if encErr == nil {
		syncErr = tmp.Sync()
	}
	closeErr := tmp.Close()
	if encErr != nil {
		os.Remove(tmpPath)
		return encErr
	}
	if syncErr != nil {
		os.Remove(tmpPath)
		return syncErr
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return closeErr
	}

	return os.Rename(tmpPath, configPath()) // atomic swap
}

// SetPersistedServer persists url (trailing slashes trimmed) as the default
// server URL in $PARLAY_STATE_HOME/config.json. An empty url clears the
// persisted value, same as config.ts's setPersistedServer(undefined).
func SetPersistedServer(url string) error {
	cfg := readPersistedConfig()
	cfg.Server = strings.TrimRight(url, "/")
	return writePersistedConfig(cfg)
}

// PersistedServerURL returns the persisted server URL, or "" if unset.
func PersistedServerURL() string {
	return strings.TrimSpace(readPersistedConfig().Server)
}

// ConfigFilePath returns the path to the persisted config file.
func ConfigFilePath() string {
	return configPath()
}

// ServerURL resolves the server base URL, trimming trailing slashes.
// Precedence: PARLAY_SERVER env var (explicit, per-shell override) >
// persisted config (~/.parlay/config.json, set via `parlay remote set`) >
// coded default. Read lazily on every call — mirrors config.ts's serverUrl()
// — so a PARLAY_SERVER set after process start (e.g. in a test) is honored.
func ServerURL() string {
	if env := strings.TrimSpace(os.Getenv("PARLAY_SERVER")); env != "" {
		return strings.TrimRight(env, "/")
	}
	if persisted := PersistedServerURL(); persisted != "" {
		return strings.TrimRight(persisted, "/")
	}
	return DefaultServer
}

// ServerSourceKind names which precedence level ServerSource() resolved from.
type ServerSourceKind string

const (
	SourceEnv     ServerSourceKind = "env"
	SourceConfig  ServerSourceKind = "config"
	SourceDefault ServerSourceKind = "default"
)

// ServerSourceInfo is the resolved server URL plus which source it came from —
// used by `parlay doctor` / `parlay remote` to explain resolution to the user.
type ServerSourceInfo struct {
	Source ServerSourceKind
	URL    string
}

// ServerSource reports which precedence level is currently in effect.
func ServerSource() ServerSourceInfo {
	if env := strings.TrimSpace(os.Getenv("PARLAY_SERVER")); env != "" {
		return ServerSourceInfo{SourceEnv, strings.TrimRight(env, "/")}
	}
	if persisted := PersistedServerURL(); persisted != "" {
		return ServerSourceInfo{SourceConfig, persisted}
	}
	return ServerSourceInfo{SourceDefault, DefaultServer}
}

// SpawnAccountEnv is the env override for the default ccjuggler account,
// read by bin/parlay-spawn under the same name.
const SpawnAccountEnv = "PARLAY_SPAWN_DEFAULT_ACCOUNT"

// spawnAccountRe matches a `spawnAccount = <value>` assignment; the value is
// unquoted by trimSpawnAccountValue below.
var spawnAccountRe = regexp.MustCompile(`^\s*spawnAccount\s*=\s*(.*)$`)

// tomlTableRe matches a `[table]` / `[[array]]` header — where the top-level
// scope this reader cares about ends.
var tomlTableRe = regexp.MustCompile(`^\s*\[`)

// spawnAccountConfigPath is the TOML config bin/parlay-spawn reads. Note this
// is NOT configPath(): the persisted CLI config is config.json, while
// spawnAccount has always lived in config.toml alongside it. Both hang off
// StateHome(). config.toml is hand-edited today — the `parlay spawn-account
// set/show/clear` verbs skills/parlay-spawn/SKILL.md used to advertise were
// never ported to Go, and writing the key back needs a TOML writer that
// preserves the existing [spawn] table (robots-ni5p). This is the read half.
func spawnAccountConfigPath() string {
	return filepath.Join(StateHome(), "config.toml")
}

// SpawnAccount resolves the default ccjuggler account name to spawn agents
// under, or "" when none is configured.
//
// Precedence is bin/parlay-spawn's, exactly: a non-empty
// PARLAY_SPAWN_DEFAULT_ACCOUNT > `spawnAccount` in config.toml > empty. An
// env var set but empty falls through to the config file, matching the bash
// `[ -z ... ]` test rather than the header comment above it, which claims an
// empty value disables the lookup and does not.
//
// A missing or unreadable config file resolves to "" — the same fall-through
// the bash reader gets from its `|| true`-guarded python3 call. This matters:
// an account that fails to resolve is a *louder* failure than no account at
// all (the spawner exits non-zero on an unresolvable token), so guessing from
// a half-parsed file would be worse than not guessing.
//
// KNOWN LIMITATION on malformed files. This is a line scanner that stops at
// the first table header, not a validating parser, so it only agrees with
// tomllib about malformation AT OR BEFORE the spawnAccount line — an
// unterminated quote on that line resolves to "", like tomllib. Malformation
// AFTER it is never seen: the scanner has already returned, so
// `spawnAccount = "acc2"` followed by an unclosed `[spawn` yields "acc2"
// where tomllib raises and bash resolves "". A top-level multi-line array
// diverges the other way — an element line starting with `[` reads as a table
// header, so the key beyond it is missed where tomllib would find it. Neither
// shape occurs in the flat config.toml this repo writes, and closing them
// means the TOML parser decision #4 of the brief rejected.
func SpawnAccount() string {
	if env := strings.TrimSpace(os.Getenv(SpawnAccountEnv)); env != "" {
		return env
	}
	return spawnAccountFromTOML(spawnAccountConfigPath())
}

// spawnAccountFromTOML reads the top-level `spawnAccount` string out of a
// TOML file. This is deliberately a single-key scanner, not a TOML parser:
// tools/cli has zero third-party dependencies (see go.mod) and the one value
// needed here is a top-level scalar. It stops at the first table header so a
// `spawnAccount` nested under some `[section]` is never mistaken for the
// top-level key python3's tomllib.get("spawnAccount") would return.
func spawnAccountFromTOML(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if tomlTableRe.MatchString(line) {
			return "" // left the top-level table; the key was not there
		}
		m := spawnAccountRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		return trimSpawnAccountValue(m[1])
	}
	return ""
}

// trimSpawnAccountValue strips a trailing comment and surrounding quotes from
// a TOML scalar. Only the basic/literal single-line string forms are handled;
// anything else (multi-line, escapes) yields whatever is between the quotes,
// which for an account name — a keychain-service suffix — is the whole
// legitimate value space.
func trimSpawnAccountValue(raw string) string {
	v := strings.TrimSpace(raw)
	// A `#` inside quotes is part of the value, so only strip a comment that
	// starts outside them.
	if !strings.HasPrefix(v, `"`) && !strings.HasPrefix(v, "'") {
		if i := strings.Index(v, "#"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		return v
	}
	quote := v[:1]
	closing := strings.Index(v[1:], quote)
	if closing < 0 {
		// Unterminated quote — a config caught mid-write, or a hand-edit that
		// dropped the closing quote. Resolve to "" rather than guessing at the
		// half-value: an account name that fails token resolution makes the
		// spawner exit non-zero, so a guess turns "config is malformed" into
		// "launch hard-fails". python3's tomllib raises here too, and the bash
		// reader's `|| true` turns that into empty.
		return ""
	}
	return v[1 : 1+closing]
}

// SpawnAccountConfigPath returns the config.toml path that holds the spawn
// account — the file bin/parlay-spawn reads. Exposed for `parlay defaults`
// to print where a value lives; the server URL's file is a different one
// (config.json, see ConfigFilePath).
func SpawnAccountConfigPath() string {
	return spawnAccountConfigPath()
}

// SetSpawnAccount persists account as the default ccjuggler spawn account in
// $PARLAY_STATE_HOME/config.toml (the same file bin/parlay-spawn reads and
// `parlay launch` resolves through SpawnAccount). An empty account clears the
// key — control returns to PARLAY_SPAWN_DEFAULT_ACCOUNT, then to no account.
//
// Only the top-level `spawnAccount` line is touched; everything else in the
// file — the [spawn] table, comments, later sections — is preserved
// byte-for-byte. Rewriting the file from scratch is the exact failure
// robots-ni5p flagged as missing on the read then: the existing [spawn]
// table must survive a write, or a spawner reading beads_required/launcher
// under it drifts from the operator's intent. Written atomically via a
// same-dir .tmp + rename, the same publication discipline writePersistedConfig
// uses, so an interrupted write never leaves the line-scanner a file naming a
// different account.
func SetSpawnAccount(account string) error {
	dir := StateHome()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := spawnAccountConfigPath()
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	next := setSpawnAccountInTOML(string(body), account)
	if next == string(body) {
		return nil // a no-op (e.g. clearing an already-clear file) never rewrites
	}
	tmp, err := os.CreateTemp(dir, ".config.toml.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	// Sync before Close, and before the rename — a rename that lands ahead of
	// the data publishes a correctly-named config holding nothing, the exact
	// failure an atomic swap is supposed to rule out.
	var writeErr error
	_, writeErr = tmp.WriteString(next)
	var syncErr error
	if writeErr == nil {
		syncErr = tmp.Sync()
	}
	closeErr := tmp.Close()
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return writeErr
	}
	if syncErr != nil {
		_ = os.Remove(tmpPath)
		return syncErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return closeErr
	}

	return os.Rename(tmpPath, path) // atomic swap
}

// setSpawnAccountInTOML rewrites body with the top-level `spawnAccount` key
// set to account. An existing top-level line is replaced in place; a missing
// one is inserted before the first table header — a key under a `[section]`
// is a DIFFERENT key than the one spawnAccountFromTOML reads, so appending
// after the [spawn] table would silently resolve to nothing. account == ""
// drops the line. The result is returned; callers compare it to the input to
// skip a no-op write.
func setSpawnAccountInTOML(body, account string) string {
	lines := []string{}
	if body != "" {
		lines = strings.Split(body, "\n")
	}
	out := make([]string, 0, len(lines)+1)
	replaced := false
	insertAt := -1
	for i, line := range lines {
		if insertAt < 0 && !replaced && tomlTableRe.MatchString(line) {
			insertAt = i
		}
		if !replaced && insertAt < 0 && spawnAccountRe.MatchString(line) {
			if account != "" {
				out = append(out, spawnAccountLine(account))
			}
			replaced = true
			continue
		}
		out = append(out, line)
	}
	if !replaced && account != "" {
		if insertAt < 0 {
			insertAt = len(out)
		}
		res := make([]string, 0, len(out)+1)
		res = append(res, out[:insertAt]...)
		res = append(res, spawnAccountLine(account))
		res = append(res, out[insertAt:]...)
		out = res
	}
	return strings.Join(out, "\n")
}

// spawnAccountLine renders `spawnAccount = "<account>"` in TOML basic-string
// form, escaping the two characters that would otherwise end the string.
func spawnAccountLine(account string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(account)
	return `spawnAccount = "` + escaped + `"`
}
