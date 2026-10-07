// The control-socket half of package relayctl: one bounded GET over the
// relay's Unix socket, and the three typed reads built on it. Every read is
// read-only by construction — the socket also serves POST /register and POST
// /unregister, and nothing in this file can reach them.
//
// The reader half of the file (types, runtime-dir resolution, spool reads) is
// relayctl.go.
package relayctl

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// ReadHealth answers GET /health when the relay's control socket is live.
// ok=false covers every "could not ask" — no socket, refused, wedged,
// undecodable, or a non-2xx answer from a build that predates the route.
func ReadHealth() (Health, bool) {
	return get[Health]("/health")
}

// ReadAgents answers GET /agents. ok=false is "the relay did not answer",
// never "the relay polls nobody".
func ReadAgents() (Agents, bool) {
	return get[Agents]("/agents")
}

// ReadDelivery answers GET /delivery, narrowed to agentID when non-empty (the
// relay filters, so the limit applies after filtering).
func ReadDelivery(limit int, agentID string) (Delivery, bool) {
	path := "/delivery"
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	if agentID != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + "agent=" + agentID
	}
	return get[Delivery](path)
}

// get issues one bounded GET over the control socket, decoding into out. A
// transport is created per call and its idle connections closed, so a
// diagnostic that runs once cannot leave a held-open socket behind.
func get[T any](path string) (T, bool) {
	var out T
	sock := SockPath()
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: ControlTimeout}
			return d.DialContext(ctx, "unix", sock)
		},
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: ControlTimeout}

	resp, err := client.Get("http://relay" + path)
	if err != nil {
		return out, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, false
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, false
	}
	return out, true
}
