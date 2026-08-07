---
"@parlay/cli": patch
---

**The printed Monitor arm-command no longer executes the ticket title (robots-2h4n).**

`parlay claim <task-id>` hands a fresh agent one startup command to paste:

```
Monitor({ command: "PARLAY_SERVER=… parlay listen --agent … --name \"<name>\" …", persistent: true })
```

That `<name>` is the ticket **title, verbatim**, and a title is arbitrary prose.
Inside double quotes a shell keeps `$(…)`, backticks and `$VAR` live, so a title
that merely *mentions* command substitution got substituted the moment the agent
did what the brief told it to do. The ticket that exposed this was its own
reproducer: its title contains `$( )`, which the pasted line ran as an empty
command substitution and silently dropped from the registered agent name. A title
carrying a `"` was worse — it closed the JS string literal early and the pasted
`Monitor({…})` call re-parsed as something else entirely.

Every value interpolated into the arm-command is now POSIX single-quoted, which
makes each character in it inert, and the whole command is then rendered as a
properly-escaped string literal for the `Monitor({})` call. The printed line reads
more plainly as a result — `--name 'Ship the widget'` rather than
`--name \"Ship the widget\"`.

Fixed at all three sites that print this command: `parlay claim`'s brief, the Go
`composeStartupPrompt`, and `bin/parlay-spawn`'s default startup prompt.

The new tests refuse to be vacuous. Each one round-trips a deliberately hostile
title — `$( )`, a backtick, `$VAR`, a `"` and a `'` — through a **real** shell and
asserts the name comes back byte-identical; the shell test additionally runs the
pre-fix double-quoted spelling and fails if *that* form survives too, so a test
that would pass against the bug is itself a failure.
