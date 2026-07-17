package main

import _ "embed"

// ── The embedded default manifest (last-resort fallback) ────────────────────────
//
// default_commands.json is //go:embed-ded into the binary as the fallback command
// set, so a bare binary with no PARLAY_COMMANDS file still works. This is the ONLY
// place commands touch the binary — and it is a fallback, not the source of truth
// (contract §Loading precedence: request > file > embedded). It encodes exactly
// today's ten builtins; interp_test.go proves the interpreter reproduces the old
// runAction actionList for each.

//go:embed default_commands.json
var defaultManifestJSON []byte

// embeddedManifest parses the embedded default. It panics on failure because a
// broken embed is a build-time programmer error, not a runtime condition — the
// binary must never ship with an invalid fallback.
func embeddedManifest() *Manifest {
	man, err := parseManifest(defaultManifestJSON)
	if err != nil {
		panic("embedded default_commands.json is invalid: " + err.Error())
	}
	return man
}
