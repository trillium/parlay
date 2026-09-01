package commands

import (
	"fmt"
	"regexp"
	"strings"
)

// splitRepo splits "owner/name" (tolerating a trailing .git and any leading
// host/path segments). Pure — repo resolution lives in resolveMergeGateRepo.
func splitRepo(repo string) (owner, name string, ok bool) {
	repo = strings.TrimSuffix(strings.TrimSpace(repo), ".git")
	parts := strings.Split(strings.Trim(repo, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-1] == "" || parts[len(parts)-2] == "" {
		return "", "", false
	}
	return parts[len(parts)-2], parts[len(parts)-1], true
}

// resolveMergeGateRepo decides, ONCE, which repository this run is about.
//
// This is the robots-g4qz defect. Letting `gh` pick the repo implicitly is
// not neutral: gh's base-repo resolution deliberately prefers a remote named
// `upstream` over `origin` (its remote sort order is upstream, github,
// origin, …). In a fork clone — which is every clone the fleet works in,
// origin=trillium/<repo> with an `upstream` remote pointing at the project
// it was forked from — `parlay merge-gate 2` therefore read UPSTREAM's PR #2
// while the mechanic was asking about the fork's. The numbers collide freely
// between the two repositories, and the failure is silent: the gate prints a
// perfectly well-formed verdict about somebody else's pull request. Worst
// case it is exit 0 "PR is already MERGED — nothing left to gate" for an
// upstream PR that landed months ago, which is a gate that fails OPEN on an
// unreviewed, still-open fork PR. That is the exact thing this verb exists
// to prevent.
//
// Precedence, highest first:
//
//  1. an explicit --repo — the caller said it, so it wins outright;
//  2. the `origin` remote — for the fleet, origin is where the PR under
//     review lives, and it is the same remote the mechanic contract's
//     `git branch -r --contains <sha>` proof checks against;
//  3. gh's own resolution — only for a checkout with no usable origin (or no
//     git repo at all), where gh's guess is the only thing available.
//
// The chosen repo and the step that chose it are both reported, so a wrong
// answer is visible in the output rather than inferred from a URL nobody
// reads.
func resolveMergeGateRepo(explicit string) (repo, source string, err error) {
	if strings.TrimSpace(explicit) != "" {
		if _, _, ok := splitRepo(explicit); !ok {
			return "", "", fmt.Errorf("--repo %q is not owner/name", explicit)
		}
		return strings.TrimSuffix(strings.TrimSpace(explicit), ".git"), "--repo", nil
	}

	if res := sh("git", "remote", "get-url", "origin"); res.ok {
		if r, ok := repoFromRemoteURL(res.out); ok {
			return r, "origin remote", nil
		}
	}

	// No origin to trust. Fall back to gh, which is what this verb always did
	// — but say so, because this is the branch that can pick `upstream`.
	res := sh("gh", "repo", "view", "--json", "owner,name",
		"--jq", `.owner.login + "/" + .name`)
	if !res.ok {
		return "", "", fmt.Errorf(
			"could not determine which repository to gate (no `origin` remote here, and `gh repo view` failed: %s). Pass --repo owner/name",
			firstLine(res.err))
	}
	r := strings.TrimSpace(res.out)
	if _, _, ok := splitRepo(r); !ok {
		return "", "", fmt.Errorf("could not determine which repository to gate (`gh repo view` returned %q). Pass --repo owner/name", r)
	}
	return r, "gh default (no origin remote)", nil
}

// remoteURLRe pulls owner/name out of any git remote URL shape: the scp-like
// git@host:owner/name.git, https://host/owner/name(.git), ssh://git@host/…,
// and git://host/…. Only the last two path segments matter, so host and
// credentials are deliberately not modeled.
var remoteURLRe = regexp.MustCompile(`^(?:[a-z+]+://)?(?:[^/@]+@)?[^/:]+[:/](.+?)(?:\.git)?/?$`)

// repoFromRemoteURL converts a git remote URL to "owner/name".
func repoFromRemoteURL(url string) (string, bool) {
	m := remoteURLRe.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", false
	}
	owner, name, ok := splitRepo(m[1])
	if !ok {
		return "", false
	}
	return owner + "/" + name, true
}

// repoFromPRURL converts a PR's html_url to "owner/name" so the answer can be
// checked against the repository that was actually asked about.
func repoFromPRURL(u string) (string, bool) {
	i := strings.Index(u, "/pull/")
	if i < 0 {
		return "", false
	}
	owner, name, ok := splitRepo(u[:i])
	if !ok {
		return "", false
	}
	return owner + "/" + name, true
}

func repoLabel(repo string) string {
	if strings.TrimSpace(repo) == "" {
		return "the current repository"
	}
	return repo
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s = strings.TrimSpace(s); s == "" {
		return "gh returned no error text"
	}
	return s
}
