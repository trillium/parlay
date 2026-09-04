//go:build darwin

package procscan

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// byEnvImpl shells out to `ps eww` — the same inline-environment listing
// technique Gas City's own darwin process scanner uses (there is no /proc on
// macOS) — and returns the pids whose environment carries key=value as one
// of the whitespace-separated KEY=VALUE tokens `ps -e` appends after the
// command. A command containing spaces (e.g. "/bin/sh -c ...") mixes into
// the same token soup as the real env vars; that is a known coarseness of
// this technique (shared with the gascity scanner it mirrors), acceptable
// here because a false match would require another process's argv to
// literally contain "GC_SESSION_ID=<our-exact-session-id>", which is not a
// realistic collision.
func byEnvImpl(key, value string) ([]int, error) {
	out, err := exec.Command("ps", "eww", "-ax", "-o", "pid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("procscan: ps: %w", err)
	}

	want := key + "=" + value
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 1 {
			continue
		}
		for _, f := range fields[1:] {
			if f == want {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids, nil
}
