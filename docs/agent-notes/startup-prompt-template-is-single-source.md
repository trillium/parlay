# The startup-prompt template is single-source (robots-hrt2)

The agent startup-prompt template lives in ONE physical place: the regular files
`tools/cli/internal/spawn/launch-templates/{default,claim}.txt`. The repo-root
`launch-templates/{default,claim}.txt` are tracked **symlinks** to those files.

- `tools/cli/internal/spawn/prompt.go` embeds the real file with
  `//go:embed launch-templates/default.txt` — go:embed cannot follow a
  symlink, which is why the regular file lives inside the package dir and the
  repo-root path is the symlink, not the other way round.
- `composeStartupPrompt` does the `{{VAR}}` → value substitution and trims the
  template's trailing newline (bash's `content=$(cat …)` stripped it; go:embed
  preserves it, so the trim is what keeps output byte-identical to the
  pre-port bytes `TestStartupPromptMatchesBashPath` still pins).

**Edit the template in `tools/cli/internal/spawn/launch-templates/`, never the symlink.**
The repo-root symlinks stay so the templates are findable from the repo root;
nothing reads them through that path any more, now that the bash spawner and
its `load_template` are gone.

## Known bash quirk the parity deliberately does not match

`load_template`'s `${content//…/$var_value}` substitution **halves backslashes**
(`\\` → `\`) in the substituted value. This only bites when a display name has an
apostrophe, which makes `shell_quote` emit a `\` inside the Monitor arm-command.
The Go path preserves both backslashes (the correct JSON). Do not "fix" the Go
side to collapse backslashes to force parity; instead keep the parity test on
apostrophe-free names and remember the two genuinely diverge there.
