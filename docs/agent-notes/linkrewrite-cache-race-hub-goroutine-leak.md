# linkrewrite cache race / hub bridge goroutine leak

PR #229 (localhost link rewriter) added `internal/linkrewrite`'s
process-cached config (`sync.Once` + package vars `cachedHost`,
`hasResolved`) alongside a test-only `ResetCacheForTest()` that reassigns
`once = sync.Once{}` and clears the vars directly, with no synchronization.

That alone is latent, but `internal/handlers.newHub`'s broker-bridge
goroutine (events.go) is documented to "live exactly as long as the
process" — no test ever stopped it. So a bridge goroutine started by one
test could still be calling `linkrewrite.Rewrite()` (which reads the cache)
while a *later* test called `ResetCacheForTest()` (which writes it),
racing under `go test -race`. It reproduced reliably only at high `-count`
because it depends on goroutine scheduling, not test order.

Fix, in `internal/linkrewrite/linkrewrite.go` and
`internal/handlers/{events,poll}.go`:

1. Replaced `sync.Once` with a plain `sync.Mutex` guarding
   `cachedHost`/`hasResolved`/`getenv`/`runTailscaleStatus`. `sync.Once`
   cannot be reset safely from a concurrent test hook — a mutex-guarded
   `bool` can be.
2. Gave `Hub` a `Stop()` (idempotent, via `sync.Once`) that cancels the
   `broker.subscribeAll()` subscription. `subscribeAll`'s `cancel` now also
   `close()`s the channel (safe: both delete-from-map and close happen
   under the broker's own mutex, same as every `publish()` send), so the
   bridge goroutine's `for range` actually exits instead of leaking.
   Production call sites (`handlers.go`, `commands.go`) never call `Stop`
   — only tests do, via `t.Cleanup(hub.Stop)` right after `newHub(...)`.

Any future package that adds a process-cached singleton with a
test-reset hook should use a mutex (or an atomic + explicit reset flag),
never a resettable `sync.Once`. Any future long-lived bridge goroutine
started by a test helper needs an explicit stop path wired to
`t.Cleanup`, or it outlives its test and can touch shared state during a
later, unrelated test.
