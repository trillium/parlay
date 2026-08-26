# Go server ticket C3: drafts/uploads/settings (`internal/handlers`)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


C3 built on C0's already-landed `internal/store` (which already had
`DraftStore`/`SettingsStore` — only `UploadStore`, in
`internal/store/uploads.go`, was new) and is registered via its own
`handlers.RegisterData(mux, st)`, called from `main.go` alongside C1's
`handlers.Register(mux, st)` — deliberately not folded into C1's `Register`,
so this ticket never had to touch or depend on C1's broker or a later C2's
SSE hub. Two things `docs/api-contract.md` doesn't pin down, decided here:

- **`GET`/`PUT` share one mux registration per path** (`handleDraft`,
  `handleSettings`, each switching on `r.Method` internally) rather than two
  separate `handleFunc` calls on the same pattern — `net/http.ServeMux`
  panics on registering the exact same pattern twice, and C1's handlers
  never hit this because each of their routes is single-method.
- **Uploads need a serving route the contract doesn't document.** `POST
  /api/chat/upload` returns `{ok, url}`, and `packages/client/src/
  attachments.ts` renders that `url` directly as an `<img src>` — so
  something on this server has to answer that URL. `UploadStore` saves each
  file under `<state-dir>/uploads/<random-hex><ext>` (never the
  client-supplied filename, which is discarded except for its sanitized
  extension) and `handleServeUpload` is mounted at the same
  `/api/chat/uploads/` prefix the upload response's `url` is rooted at, so
  the returned URL is always directly `GET`-able. Image type is verified
  server-side via `http.DetectContentType` sniffing on the actual bytes
  (not the client-supplied `Content-Type` header), capped at 10MB per the
  contract's documented client-side UI copy, now also enforced server-side.
  `handleServeUpload` re-sniffs those same bytes at serve time to set the
  response `Content-Type` (`http.ServeContent`, not `http.ServeFile`'s
  extension-based `mime.TypeByExtension`), and `UploadStore.Save`'s kept
  extension is allow-listed to image extensions only
  (`png|jpg|jpeg|gif|webp|bmp`) — together these mean a served upload's
  declared type is always derived from its real bytes, never from a
  client-supplied filename or extension.
