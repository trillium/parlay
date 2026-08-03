package commands

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

func validServerURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

// Remote ports commands-remote.ts's cmdRemote: show/persist/clear the
// default server URL in $PARLAY_STATE_HOME/config.json. PARLAY_SERVER still
// wins when set — this only fills in the fallback (see internal/config).
func Remote(argv []string) {
	if helpWanted("remote", argv) {
		return
	}
	r := args.Parse("remote", argv, nil, nil)
	var sub, url string
	if len(r.Positionals) > 0 {
		sub = r.Positionals[0]
	}
	if len(r.Positionals) > 1 {
		url = r.Positionals[1]
	}

	switch sub {
	case "":
		info := config.ServerSource()
		fmt.Printf("%s (source: %s)\n", info.URL, info.Source)

	case "set":
		if url == "" {
			httpc.Die("parlay remote set: url required, e.g. parlay remote set http://mini1:31337", config.ExitUsage)
			return
		}
		if !validServerURL(url) {
			httpc.Die(fmt.Sprintf("parlay remote set: %q is not a valid http(s) URL", url), config.ExitUsage)
			return
		}
		if err := config.SetPersistedServer(url); err != nil {
			httpc.Die(fmt.Sprintf("parlay remote set: %v", err), config.ExitRuntime)
			return
		}
		fmt.Printf("persisted default server: %s (%s)\n", strings.TrimRight(url, "/"), config.ConfigFilePath())

	case "clear":
		if err := config.SetPersistedServer(""); err != nil {
			httpc.Die(fmt.Sprintf("parlay remote clear: %v", err), config.ExitRuntime)
			return
		}
		fmt.Printf("cleared persisted default server (%s)\n", config.ConfigFilePath())

	default:
		httpc.Die(fmt.Sprintf("parlay remote: unknown subcommand %q — expected \"set <url>\" or \"clear\"", sub), config.ExitUsage)
	}
}
