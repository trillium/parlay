package handlers

import (
	"io"
	"net/http"
	"strings"

	"parlay/go-server/internal/store"
)

// maxUploadBytes matches the 10MB cap the client's own UI copy documents
// (docs/api-contract.md §Uploads) — enforced here too rather than trusted to
// the client alone, since this is a data boundary the server owns.
const maxUploadBytes = 10 << 20

// uploadURLPrefix is the path handleUpload's returned `url` is rooted at and
// handleServeUpload is registered under: url == uploadURLPrefix + the
// filename store.UploadStore.Save returned, so a `url` field from an upload
// response is always directly resolvable by GET without any other mapping.
const uploadURLPrefix = "/api/chat/uploads/"

type uploadResponse struct {
	OK  bool   `json:"ok"`
	URL string `json:"url,omitempty"`
}

// handleUpload implements POST /api/chat/upload. Per the contract, callers
// only ever check `!res.ok || !res.url` and surface no server-provided error
// message on failure, so every rejection path below responds with a bare
// {"ok": false} rather than inventing an {error} field nothing reads.
func handleUpload(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
		if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
			writeJSON(w, uploadResponse{OK: false})
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, uploadResponse{OK: false})
			return
		}
		defer file.Close()

		data, err := io.ReadAll(file)
		if err != nil {
			writeJSON(w, uploadResponse{OK: false})
			return
		}

		// Sniffed from content, not trusted from the client-supplied
		// filename or Content-Type header — "images only" per the contract.
		if !strings.HasPrefix(http.DetectContentType(data), "image/") {
			writeJSON(w, uploadResponse{OK: false})
			return
		}

		name, err := st.Uploads.Save(header.Filename, data)
		if err != nil {
			writeJSON(w, uploadResponse{OK: false})
			return
		}
		writeJSON(w, uploadResponse{OK: true, URL: uploadURLPrefix + name})
	}
}

// handleServeUpload implements GET /api/chat/uploads/<name> — not itself a
// documented route in docs/api-contract.md, but required for the `url`
// handleUpload hands back to ever resolve to anything: callers render it
// directly as an <img src> (packages/client/src/attachments.ts), so this
// server must serve what it just accepted.
func handleServeUpload(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, uploadURLPrefix)
		path, err := st.Uploads.Path(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, path)
	}
}
