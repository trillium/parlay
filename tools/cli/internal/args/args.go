// Package args is the parlay CLI's argument parser.
//
// Hand-ported from packages/cli/src/args.ts — not a generic flag package.
// docs/scope-go-cli.md §5 item 1: some call sites (`send`, `nickname`) parse
// an arbitrary --<anything> token as a value (e.g. `send --mayor "msg"` ->
// target "mayor"), which no stdlib/third-party flag package expresses
// directly. Parse here stays a faithful port of the exact loop semantics;
// commands needing the dynamic-flag behavior do their own pre-parsing before
// or instead of calling Parse, same as args.ts's call sites do.
package args

import (
	"fmt"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// Value holds one parsed option: either a boolean flag (Present, no value)
// or a value-taking flag (Present + Str).
type Value struct {
	Present bool
	Str     string
}

// Result is the outcome of Parse: leftover positional args, plus a map of
// recognized flags to their values.
type Result struct {
	Positionals []string
	Opts        map[string]Value
}

// Bool reports whether a boolean flag was present.
func (r Result) Bool(flag string) bool {
	return r.Opts[flag].Present
}

// String returns a value-flag's string value and whether it was present.
func (r Result) String(flag string) (string, bool) {
	v, ok := r.Opts[flag]
	if !ok {
		return "", false
	}
	return v.Str, true
}

// Parse parses cmd's args. flags are boolean (no value); valueFlags consume
// the next token as their value. Any other "-"-prefixed token is an unknown
// flag and dies loud with EXIT_USAGE — matching args.ts's fail-fast
// behavior. "--" ends flag parsing; every token after it is a positional,
// even one that looks like a flag.
func Parse(cmd string, argv []string, flags []string, valueFlags []string) Result {
	isFlag := make(map[string]bool, len(flags))
	for _, f := range flags {
		isFlag[f] = true
	}
	isValueFlag := make(map[string]bool, len(valueFlags))
	for _, f := range valueFlags {
		isValueFlag[f] = true
	}

	positionals := []string{}
	opts := make(map[string]Value)
	noMoreFlags := false

	for i := 0; i < len(argv); i++ {
		a := argv[i]

		if noMoreFlags || len(a) == 0 || a[0] != '-' {
			positionals = append(positionals, a)
			continue
		}
		if a == "--" {
			noMoreFlags = true
			continue
		}
		if isFlag[a] {
			opts[a] = Value{Present: true}
			continue
		}
		if isValueFlag[a] {
			i++
			if i >= len(argv) {
				httpc.Die(fmt.Sprintf("parlay %s: flag %s requires a value", cmd, a), config.ExitUsage)
			}
			opts[a] = Value{Present: true, Str: argv[i]}
			continue
		}
		httpc.Die(fmt.Sprintf("parlay %s: unknown flag %q", cmd, a), config.ExitUsage)
	}

	return Result{Positionals: positionals, Opts: opts}
}
