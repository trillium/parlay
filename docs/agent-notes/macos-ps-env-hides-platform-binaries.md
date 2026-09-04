# macOS hides env vars from `ps eww` for sealed platform binaries

`internal/procscan` (the `gc-teardown` seam for #203) identifies a
session's process by scanning the process table for a live process whose
environment contains `GC_SESSION_ID=<session-id>` — an exact content
match, immune to pid-reuse. `scan_darwin.go` implements this with
`ps eww -ax -o pid=,command=`, the same technique gascity's own
`internal/runtime/proctable/scan_darwin.go` uses.

On current macOS (confirmed on 26.x, "platform binaries" under the sealed
system volume — `/bin/sleep`, `/bin/zsh`, etc.), `ps eww` does **not**
expose that process's environment variables, even as root
(`sudo -n ps eww`). This is not a Bash-tool sandbox artifact (reproduces
identically with the sandbox disabled) and not a permissions issue in the
usual sense — a Homebrew-installed binary (e.g. `/opt/homebrew/bin/jq`)
shows its full environment under the identical `ps eww` invocation. The
restriction is keyed on the binary's provenance (sealed system volume vs.
not), not on the caller's privilege or `ps`'s flags. It would equally
affect gascity's own scanner, since it uses the same technique with no
fallback — this is a platform fact to route around, not a defect in
either implementation.

**Consequence for any test that needs a real long-running fixture
process on darwin to prove an env-matching process scanner works:**
`/bin/sleep &` is invisible to the scanner and looks like "the pid never
appeared" — a false negative that has nothing to do with the scanner's
correctness. Use a self-re-exec fixture instead: have the test binary
re-exec itself (`os.Executable()` + `exec.Command(exe)`) with a
sentinel env var set, and gate `TestMain` to park the child forever
(`time.Sleep(24 * time.Hour)` — **not** an empty `select{}`, which Go's
runtime deadlock detector kills immediately in a single-goroutine
process) instead of running the suite. A `go test`-compiled binary is
not a sealed platform binary, so its environment stays visible to
`ps eww` like any other locally-built executable.

See `internal/procscan/procscan_test.go` (`spawnMarked`/`TestMain`) and
`internal/commands/gc_teardown_test.go` (`spawnMarkedProcess`, mirrored
into that package's own `TestMain` since a package can have only one) for
the working pattern.
