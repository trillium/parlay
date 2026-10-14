# Command-file example

This directory is a command file you copy. The eval engine does not load it
from the repo.

The engine reads commands in three layers:

1. A per-request `commands` body replaces the whole set for that request.
2. A command file adds onto the embedded defaults.
3. The embedded defaults are the fallback. The shipped `submit` phrases are
   `send it`, `submit`, and `submit that`.

[`submit-phrases.json`](submit-phrases.json) is a `parlay.commands/v1` file
whose `submit` command lists more phrases. Copy it, then point the engine at
the copy:

```sh
cp examples/commands/submit-phrases.json "$HOME/parlay-commands.json"
export PARLAY_COMMANDS="$HOME/parlay-commands.json"
```

With `PARLAY_COMMANDS` unset, the engine reads `commands.json` in the same
directory as the binary that runs `parlay eval serve`, when that file exists.

How a file merges:

- A command id that already exists keeps the embedded mode, priority,
  platforms, and emit. Its phrases are the embedded phrases, then any phrase
  from the file that is not already there. `send it`, `submit`, and
  `submit that` stay when this example is loaded.
- A command id that is not in the embedded set is appended whole.
- Each load merges against the embedded defaults again. A phrase you take out
  of a later file stops matching. A file cannot remove a shipped phrase.

Restart `parlay eval serve` after the first copy. Later edits to that file
hot-reload.

`PARLAY_SUBMIT_ENDERS` (comma-separated) and `$PARLAY_STATE_HOME/submit-enders`
(one phrase per line) still append extra phrases to `submit` as well. Those
phrases are added after the command-file merge.
