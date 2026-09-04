//go:build linux

package procscan

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// byEnvImpl enumerates /proc directly — the same technique Gas City's own
// linux orphan scanner uses — and returns the pids whose environment
// carries key=value. A pid this process cannot read the environ of
// (permission denied, or the pid exiting between readdir and read) is
// silently excluded rather than erroring the whole scan: per the package
// doc, under-matching one pid is always safe, and a genuinely unreadable
// process table is signaled by the /proc enumeration itself failing, not by
// any single entry.
func byEnvImpl(key, value string) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("procscan: enumerating /proc: %w", err)
	}

	want := key + "=" + value
	var pids []int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 1 {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if err != nil {
			continue
		}
		for _, entry := range strings.Split(string(data), "\x00") {
			if entry == want {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids, nil
}
