package guard

// Non-vacuity proofs for the widened route scan, in both directions: a
// registration shape the scan cannot follow, and a module with nothing to follow.

import "testing"

// TestTheWidenedScanFailsLoudlyOnShapesItCannotFollow covers the
// non-vacuity requirements in both directions: a registration the scan cannot
// follow, and a registration-carrying package that fell out of the closure.
// Neither may quietly shrink what the gate examines; each must be reported.
func TestTheWidenedScanFailsLoudlyOnShapesItCannotFollow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "registration behind a receiver the scan does not recognise",
			files: map[string]string{
				"cmd/parlay-server/main.go": `package main

import "net/http"

func main() {
	router := http.NewServeMux()
	router.HandleFunc("/api/chat/forged", h)
}

func h(http.ResponseWriter, *http.Request) {}
`,
			},
			want: "receiver is not mux",
		},
		{
			name: "registration whose path argument is unresolvable",
			files: map[string]string{
				"cmd/parlay-server/main.go": `package main

import "net/http"

const notAString = 1 + 1

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc(notAString, h)
}

func h(http.ResponseWriter, *http.Request) {}
`,
			},
			want: "cannot resolve",
		},
		{
			name: "registration-carrying package outside the binary's closure",
			files: map[string]string{
				"cmd/parlay-server/main.go": `package main

func main() {}
`,
				"internal/orphan/routes.go": `package orphan

import "net/http"

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/chat/forged", h)
}

func h(http.ResponseWriter, *http.Request) {}
`,
			},
			want: "no package main in this module imports it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := scanRoutes(fakeModule(t, tc.files))
			if err != nil {
				t.Fatalf("scanRoutes: %v", err)
			}
			if !hasProblem(res.problems, tc.want) {
				t.Fatalf("expected a problem containing %q, got %v", tc.want, res.problems)
			}
		})
	}
}

// TestTheZeroRouteFatalStillCoversTheWidenedSet proves the vacuous-pass guard
// is reachable on the widened set: a module whose only registration is behind
// an unfollowable shape yields zero routes, which the gate treats as fatal
// rather than as a pass.
func TestTheZeroRouteFatalStillCoversTheWidenedSet(t *testing.T) {
	root := fakeModule(t, map[string]string{
		"cmd/parlay-server/main.go": `package main

import "net/http"

func main() {
	router := http.NewServeMux()
	router.HandleFunc("/api/chat/forged", h)
}

func h(http.ResponseWriter, *http.Request) {}
`,
	})
	res, err := scanRoutes(root)
	if err != nil {
		t.Fatalf("scanRoutes: %v", err)
	}
	if len(res.routes) != 0 {
		t.Fatalf("this test needs an empty route set to be meaningful, got %v", res.routes)
	}
	if len(res.problems) == 0 {
		t.Error("zero routes with no reported problem: the gate would fatal on the count alone, " +
			"but the scan should also have named the shape it could not follow")
	}
}
