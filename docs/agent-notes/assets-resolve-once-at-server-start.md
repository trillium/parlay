# The panel bundle is resolved once, at server start

`defaultAssetsDir()` in `packages/go-server/cmd/parlay-server/main.go` runs during
`flag.Parse()` and the answer is kept for the life of the process: with no
`-assets-dir`/`PARLAY_ASSETS_DIR` it walks up from the executable's directory and then
from the process's working directory for a checkout's `packages/client/dist`, falling
back to a bare `dist`. Nothing re-resolves later.

So on a fresh clone — where the bundle is gitignored and absent — the README's own
Quickstart order (step 2 starts the server, step 5 builds the panel) leaves that server
serving `503` forever, because it bound the `dist` fallback relative to
`packages/go-server` before the bundle existed. Rebuilding does not help; only a restart
(or building first, or an explicit `-assets-dir`) does.

Observed on 2026-10-07, fresh clone of the onboarding branch, clean `HOME`:

```
$ cd packages/go-server && go run ./cmd/parlay-server            # README step 2
parlay-server: listening on http://127.0.0.1:4242 (state dir: …, assets: dist)
$ cd packages/client && bun run build                            # README step 5, exit 0
dist/parlay-agent.js + index.html + pulse-agent.js + index.js + plugins built
$ curl -sS -o /dev/null -w '%{http_code}\n' http://localhost:4242/
503
$ # Ctrl-C, re-run step 2
parlay-server: listening on http://127.0.0.1:4242 (… assets: /tmp/fm-fresh-clone/packages/client/dist)
$ curl -sS -o /dev/null -w '%{http_code}\n' http://localhost:4242/
200
```

Two adjacent facts from the same run, both now in `docs/command-server.md`:

- a relative `-assets-dir` is resolved from the server's own working directory: started
  from `packages/go-server`, `-assets-dir ../client/dist` answers `200` while
  `-assets-dir packages/client/dist` answers `503`.
- `static.Handler` stats the resolved directory **per request**, so the fallback/`503`
  and the `404` for a directory with no `index.html` are runtime facts, not boot-time
  ones — the boot-time part is only which directory it was handed.

The 2026-09-05 ux-eval fix that added the working-directory walk-up
(`docs/ux-eval-2026-08-30.md`) addressed the `go run` temp-binary case, where the
executable-relative lookup finds nothing; it does not address a bundle built after the
server started. Do not "re-fix" this by making resolution lazy without deciding what a
mid-flight asset swap should do — the README path now restarts the server instead.
