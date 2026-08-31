# The crew-status seam has two gates, and the order between them is the rollout

Status-lift units 3–7 (epic task-4cfpv, umbrella task-4cfpv.12; PRs #195 #196
#197 #199 #200) moved agent status onto a bead-backed store plus a per-agent
event log — behind **two separate env gates on purpose**:

- `PARLAY_CREW_STORE=<beadsDir>` — enables **dual-WRITE**. `parlay status` (and
  claim's recorder) writes the legacy status file FIRST (still the operative
  record), then appends to the per-agent event log
  (`<agent-dir>/events.jsonl`, `internal/crewevents` — monotonic per-agent
  Seq, blocking flock, error-propagating; the §7.1 mitigation for
  FileRecorder's silent 250ms-flock drop), then folds into the bead store
  (`crewDualWrite`). New-pipeline failure dies `ExitRuntime` naming what
  landed.
- `PARLAY_CREW_READ_BEADS=1` — **additionally** cuts the readers over:
  `crew-state` reads status from the bead (file fallback + stderr note on
  store failure; quiet fallback on absent/attach-only bead), and `supervise`
  switches to the event-seq cursor (`.supervise-seq`, alongside the legacy
  `.supervise-marker` — different sequences, never mixed).

Both unset = byte-identical legacy behavior. The split exists so a
**shakedown** can run dual-write while every reader stays on the proven file
path. Rollout order is therefore fixed:

1. `parlay status-migrate --agents-root <dir> --apply` — replays existing
   status files into log+store (replay-never-truncate, full backup, cursor
   seeded at head so history does not re-fire). **Running it against the live
   `~/.parlay/agents` is captain-gated (robots-lor)** — the tool refuses that
   root without `--live`, and `--agents-root` has no default.
2. Set `PARLAY_CREW_STORE` — dual-write shakedown, readers on legacy.
3. Set `PARLAY_CREW_READ_BEADS=1` — flip readers.

Contract pins to know before touching any of it:

- The crew-state wire contract (exit codes 3/4/5/6, source strings, detail
  suffixes) is FROZEN and regression-tested in `crew_state_beads_test.go`;
  ~30 firstmate shell scripts parse the status file, whose bytes are pinned
  three ways in `status_projection_test.go` + `testdata/status-projection.golden`.
  Regenerating that golden to make a test pass is a breaking change.
- Readers open the store with `parlaybeads.Open`, never Init — the writer owns
  store existence.
- The spawn seam owns the per-agent gc session bead; status ATTACHES to it
  (`gc_session` stamp merged on live writes). Never mint a second per-agent
  bead — migration deliberately attaches no stamp.
- Supervise doctrine: enqueue-before-cursor-advance and
  do-not-advance-on-relay-failure hold on BOTH cursors; an existing event
  log's "nothing new" is FINAL (no legacy re-scan — the line cursor never
  advances post-cutover and a re-scan would re-fire everything).
