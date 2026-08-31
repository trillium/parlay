# commands/ — empty until a seam has a real pack command

A pack command is a directory containing `run.sh`; nested directories become
nested command words, and an optional `command.toml` overrides words or
entrypoint (pack-spec §1.2.11):

```text
commands/parlay/status/run.sh   →  a `parlay status` pack command
```

Private helper scripts that are not entrypoints belong under `../assets/`,
not here. This file is not a command directory and is ignored by the loader.
