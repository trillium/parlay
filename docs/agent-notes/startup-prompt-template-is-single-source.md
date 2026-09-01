# The startup-prompt template is single-source (robots-hrt2)

The agent startup-prompt template lives in ONE physical place: the regular files
`tools/parlay-bin/launch-templates/{default,claim}.txt`. The repo-root
`launch-templates/{default,claim}.txt` are tracked **symlinks** to those files.

- `bin/parlay-spawn` reads `launch-templates/default.txt` through the symlink
  (its `TEMPLATES_DIR="$BIN_DIR/../launch-templates"`), via `load_template`.
- `tools/parlay-bin/prompt.go` embeds the real file with
  `//go:embed launch-templates/default.txt` (go:embed cannot follow a symlink,
  so the regular file must live inside the package dir and the symlink must be
  on the bash side).
- `composeStartupPrompt` performs the same `{{VAR}}` → value substitution
  `load_template` does, plus trims the template's trailing newline (bash's
  `content=$(cat …)` strips it; go:embed preserves it).

**Edit the template in `tools/parlay-bin/launch-templates/`, never the symlink.**
Both paths then pick it up. `TestStartupPromptMatchesBashPath` (`prompt.go`
parity) proves byte-identical output.

## Known bash quirk the parity deliberately does not match

`load_template`'s `${content//…/$var_value}` substitution **halves backslashes**
(`\\` → `\`) in the substituted value. This only bites when a display name has an
apostrophe, which makes `shell_quote` emit a `\` inside the Monitor arm-command.
The Go path preserves both backslashes (the correct JSON). Do not "fix" the Go
side to collapse backslashes to force parity; instead keep the parity test on
apostrophe-free names and remember the two genuinely diverge there.
