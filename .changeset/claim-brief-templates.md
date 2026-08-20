---
"@parlay/cli": patch
---

**`parlay claim`'s two briefs moved into embedded templates, without re-opening
the arm-command hole (robots-xy7e).**

`claimBrief` and `claimNoWorkBrief` were long runs of `b.WriteString(...)` with
the agent-facing prose interleaved through Go control flow, which made the copy
hard to read and hard to edit. Both now render from `//go:embed` templates —
`claim_brief.md.tmpl` and `claim_no_work_brief.md.tmpl` — so the prose lives as
prose and the Go side only computes the values it interpolates. `bin/parlay-spawn`
gets the same treatment for its `--claim` startup prompt (`bin/startup_prompt.tmpl`).

The reason this needed care: the brief's Monitor arm-command carries the ticket
title verbatim, and robots-2h4n had just landed the shell-quoting that keeps a
title from executing when the agent pastes the printed line. The natural way to
write the template line —

```
Monitor({ command: "{{.MonitorCmd}}", persistent: true })
```

— silently reverts that fix. `text/template` interpolates verbatim and has no way
to escape what it substitutes, so the template can never be the place that adds
the quotes. `claimBrief` therefore hands over a value that is *already* a complete
string literal (`strconv.Quote` around a command whose every value is POSIX
single-quoted), and the template renders it **bare**.

`TestClaimBriefQuotesHostileTitle` — which round-trips a hostile title through a
real `/bin/sh` — was kept as-is and catches the re-quote at the behavior level:
temporarily restoring the double-quoted template line caused it to fail, confirming
the test still has teeth against this exact regression.
