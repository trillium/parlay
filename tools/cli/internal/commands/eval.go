// parlay eval — the compiled string-evaluation engine, unified into the
// single parlay binary (task-0ke9). The engine itself lives in
// internal/evalengine (formerly the standalone packages/eval-engine module);
// this file is only the CLI seam: subcommand dispatch + flag parsing.
package commands

import (
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/evalengine"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// Eval is `parlay eval`'s entry point.
func Eval(argv []string) {
	if helpWanted("eval", argv) {
		return
	}
	sub := ""
	if len(argv) > 0 {
		sub = argv[0]
	}
	switch sub {
	case "serve":
		evalServe(argv[1:])
	default:
		httpc.Die("parlay eval: subcommand required: serve (run 'parlay eval --help')", config.ExitUsage)
	}
}

// evalServe parses `parlay eval serve [--addr <host:port>] [--push-url <url>]`
// and runs the engine's HTTP service in the foreground until killed. Flags
// win over the engine's PARLAY_EVAL_ADDR / PARLAY_EVAL_PUSH_URL env defaults.
func evalServe(argv []string) {
	var addr, pushURL string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		val := func(flag string) string {
			if eq := strings.IndexByte(a, '='); eq >= 0 {
				return a[eq+1:]
			}
			i++
			if i >= len(argv) {
				httpc.Die("parlay eval serve: "+flag+" requires a value", config.ExitUsage)
				return "" // unreachable; Die exits
			}
			return argv[i]
		}
		switch {
		case a == "--addr" || strings.HasPrefix(a, "--addr="):
			addr = val("--addr")
		case a == "--push-url" || strings.HasPrefix(a, "--push-url="):
			pushURL = val("--push-url")
		default:
			httpc.Die("parlay eval serve: unknown argument "+a, config.ExitUsage)
		}
	}
	evalengine.Serve(addr, pushURL)
}
