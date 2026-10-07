package main

import (
	"os"
	"path/filepath"
	"testing"
)

// repoAssetsDir walks up from a nested directory to find a checkout's bundle,
// which is what makes the README's `cd packages/go-server && go run
// ./cmd/parlay-server` serve the panel: `go run` executes a temp binary under
// the build cache, so the executable-relative lookup finds nothing and the
// cwd-relative one has to work.
func TestRepoAssetsDirWalksUpFromAnyDepth(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "packages", "client", "dist")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "packages", "go-server", "cmd")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := repoAssetsDir(nested); got != want {
		t.Fatalf("repoAssetsDir(%q) = %q, want %q", nested, got, want)
	}
	if got := repoAssetsDir(root); got != want {
		t.Fatalf("repoAssetsDir at the root = %q, want %q", got, want)
	}
}

// No bundle anywhere up the chain yields "" so the caller can fall back to a
// bare "dist" (and the 503 that explains the build command) rather than
// inventing a path.
func TestRepoAssetsDirReturnsEmptyWhenNoBundleExists(t *testing.T) {
	// A directory whose chain contains no packages/client/dist.
	if got := repoAssetsDir(filepath.Join(t.TempDir(), "nested", "deeper")); got != "" {
		t.Fatalf("repoAssetsDir = %q, want \"\"", got)
	}
}

// defaultAssetsDir must resolve to a real bundle for the documented Quickstart
// invocation. It cannot be asserted for equality with the checkout's dist
// (this test binary lives elsewhere), but it must not silently fall back to a
// bare relative "dist" while a checkout bundle is visible from the cwd.
func TestDefaultAssetsDirNeverReturnsBareDistInsideACheckout(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "packages", "client", "dist")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(root, "packages", "go-server")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(inner); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	if got := defaultAssetsDir(); got != want {
		// macOS hands back /var/folders/... from TempDir but Getwd resolves
		// the symlink to /private/var/...; compare the resolved forms.
		resolved, rerr := filepath.EvalSymlinks(want)
		if rerr != nil || got != resolved {
			t.Fatalf("defaultAssetsDir() = %q, want %q (resolved %q)", got, want, resolved)
		}
	}
}
