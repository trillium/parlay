package linkrewrite

import (
	"errors"
	"testing"
)

var errNotFoundForTest = errors.New("tailscale not found (test)")

func withHost(t *testing.T, value string) {
	t.Helper()
	restore := SetGetenvForTest(func(key string) string {
		if key == "PARLAY_PUBLIC_HOST" {
			return value
		}
		return ""
	})
	ResetCacheForTest()
	t.Cleanup(func() {
		restore()
		ResetCacheForTest()
	})
}

func TestRewriteUnconfiguredIsNoop(t *testing.T) {
	withHost(t, "")
	in := "see http://localhost:4242/foo"
	if got := Rewrite(in); got != in {
		t.Fatalf("expected unchanged text, got %q", got)
	}
}

func TestRewriteConfiguredHostRewritesLocalhost(t *testing.T) {
	withHost(t, "macbook")
	got := Rewrite("see http://localhost:4242/foo")
	want := "see http://macbook:4242/foo"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRewriteConfiguredHostRewrites127001(t *testing.T) {
	withHost(t, "macbook")
	got := Rewrite("see http://127.0.0.1:31337/bar")
	want := "see http://macbook:31337/bar"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRewritePreservesPathQueryAndPort(t *testing.T) {
	withHost(t, "100.100.100.100")
	got := Rewrite("http://localhost:4242/a/b?x=1&y=2#frag")
	want := "http://100.100.100.100:4242/a/b?x=1&y=2#frag"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRewriteMultipleLinksInOneText(t *testing.T) {
	withHost(t, "macbook")
	got := Rewrite("http://localhost:4242/a and http://127.0.0.1:9999/b")
	want := "http://macbook:4242/a and http://macbook:9999/b"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRewriteDoesNotMatchLocalhostSubdomain(t *testing.T) {
	withHost(t, "macbook")
	in := "see http://localhost.evil.com:4242/foo"
	if got := Rewrite(in); got != in {
		t.Fatalf("expected unchanged text (host boundary), got %q", got)
	}
}

func TestRewriteDoesNotTouchNonLocalhostHost(t *testing.T) {
	withHost(t, "macbook")
	in := "see https://example.com:4242/foo and http://192.168.1.5:4242/bar"
	if got := Rewrite(in); got != in {
		t.Fatalf("expected unchanged text, got %q", got)
	}
}

func TestRewriteEmptyTextIsNoop(t *testing.T) {
	withHost(t, "macbook")
	if got := Rewrite(""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestRewriteAutoUsesTailscaleShortName(t *testing.T) {
	restoreEnv := SetGetenvForTest(func(key string) string {
		if key == "PARLAY_PUBLIC_HOST" {
			return "auto"
		}
		return ""
	})
	restoreTS := SetTailscaleStatusForTest(func() ([]byte, error) {
		return []byte(`{"Self":{"DNSName":"macbook.example-tailnet.ts.net."}}`), nil
	})
	ResetCacheForTest()
	t.Cleanup(func() {
		restoreEnv()
		restoreTS()
		ResetCacheForTest()
	})

	got := Rewrite("http://localhost:4242/x")
	want := "http://macbook:4242/x"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRewriteAutoFailsOpenWhenTailscaleUnavailable(t *testing.T) {
	restoreEnv := SetGetenvForTest(func(key string) string {
		if key == "PARLAY_PUBLIC_HOST" {
			return "auto"
		}
		return ""
	})
	restoreTS := SetTailscaleStatusForTest(func() ([]byte, error) {
		return nil, errNotFoundForTest
	})
	ResetCacheForTest()
	t.Cleanup(func() {
		restoreEnv()
		restoreTS()
		ResetCacheForTest()
	})

	in := "http://localhost:4242/x"
	if got := Rewrite(in); got != in {
		t.Fatalf("expected fail-open unchanged text, got %q", got)
	}
}
