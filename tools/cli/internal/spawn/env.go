package spawn

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// keyRe matches a valid shell identifier key, mirroring bash's
// `^[A-Za-z_][A-Za-z0-9_]*$` guard in bin/parlay-spawn.
var keyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// sourceDotEnv ports bin/parlay-spawn's .env block (lines 507–519) as-is,
// per docs/scope-go-spawn.md §4: a genuinely static, line-by-line parse with
// no shell execution, no value unquoting, no inline-comment stripping, and
// no ${VAR} expansion. Each valid line is forwarded VERBATIM (not
// reconstructed key=value) as one env entry — this matches bash's
// `PROJECT_ENV_FLAGS+=(--env "$_eline")` exactly, quoted values and all.
// A line whose key fails the identifier regex is silently dropped, same as
// bash's `if` with no `else` (line 513) — a known, deliberately-ported gap.
func sourceDotEnv(cwd string) (envLines []string, count int, err error) {
	path := cwd + "/.env"
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, _, _ := strings.Cut(line, "=")
		if !keyRe.MatchString(key) {
			continue
		}
		envLines = append(envLines, line)
		count++
	}
	return envLines, count, scanner.Err()
}

// filterDirenvEnv applies bin/parlay-spawn's .envrc filter (lines 524–530)
// to the KEY=VALUE lines direnv prints against a minimal baseline env,
// dropping the baseline's own vars (DIRENV_*, HOME, PATH, PWD, SHLVL, _) and
// blank keys so only the project's own additions are forwarded. Split out
// as a pure function so it's testable without actually invoking direnv.
func filterDirenvEnv(lines []string) (envLines []string, count int) {
	for _, line := range lines {
		key, _, _ := strings.Cut(line, "=")
		switch {
		case key == "":
			continue
		case strings.HasPrefix(key, "DIRENV_"):
			continue
		case key == "HOME" || key == "PATH" || key == "PWD" || key == "SHLVL" || key == "_":
			continue
		}
		envLines = append(envLines, line)
		count++
	}
	return envLines, count
}

// sourceEnvrc ports bin/parlay-spawn's .envrc block (lines 521–532): only
// runs if direnv is on PATH, and only if cwd/.envrc exists — otherwise it
// silently no-ops, matching bash (no message either way, per §4's noted
// gap). Runs direnv against a clean baseline env (HOME/PATH only) so the
// output is (approximately) just what the project's .envrc added, never a
// blanket forward of the caller's ambient env.
func sourceEnvrc(cwd string) (envLines []string, count int, ranDirenv bool, err error) {
	if _, statErr := os.Stat(cwd + "/.envrc"); statErr != nil {
		return nil, 0, false, nil
	}
	direnvPath, lookErr := exec.LookPath("direnv")
	if lookErr != nil {
		return nil, 0, false, nil
	}

	cmd := exec.Command(direnvPath, "exec", cwd, "env")
	cmd.Env = []string{"HOME=" + os.Getenv("HOME"), "PATH=" + os.Getenv("PATH")}
	out, runErr := cmd.Output()
	if runErr != nil {
		return nil, 0, true, fmt.Errorf("direnv exec %s env: %w", cwd, runErr)
	}

	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	envLines, count = filterDirenvEnv(lines)
	return envLines, count, true, nil
}
