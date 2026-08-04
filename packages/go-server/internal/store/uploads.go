package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// UploadStore persists uploaded attachment files (POST /api/chat/upload,
// docs/api-contract.md §Uploads) as plain files under a dedicated "uploads"
// subdirectory of the state dir. A client-supplied filename is never used
// as-is: Save generates a random, collision-proof name and keeps only a
// sanitized extension, so a crafted upload filename can never traverse or
// overwrite outside dir. The generated name is both the URL segment
// internal/handlers serves it back under and, since it maps 1:1 onto a real
// path on this same machine, satisfies the "canonical URL→filesystem
// mapping agents use to Read the files" this store's callers were built
// against (see packages/client/src/attachments.ts's comment referencing the
// real server's uploads.ts, which this package cannot read — see this
// repo's CLAUDE.md).
type UploadStore struct {
	dir string
}

func openUploadStore(dir string) (*UploadStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create uploads dir %s: %w", dir, err)
	}
	return &UploadStore{dir: dir}, nil
}

// safeExtRe allow-lists the extension kept from a client-supplied filename to
// a fixed set of image extensions; anything else (including no match, e.g.
// no extension, or a non-image extension like .html/.svg/.js) is dropped
// rather than echoed into a path. This keeps the on-disk/served extension
// consistent with the image-only content check handleUpload performs, so a
// served upload's Content-Type (see handleServeUpload) can never be coaxed
// into a non-image type via extension alone.
var safeExtRe = regexp.MustCompile(`(?i)^\.(png|jpg|jpeg|gif|webp|bmp)$`)

// Save writes data under a new random filename derived from origName's
// (sanitized) extension and returns that filename — never origName itself.
func (us *UploadStore) Save(origName string, data []byte) (filename string, err error) {
	ext := filepath.Ext(origName)
	if !safeExtRe.MatchString(ext) {
		ext = ""
	}

	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate upload id: %w", err)
	}
	name := hex.EncodeToString(id[:]) + strings.ToLower(ext)

	if err := os.WriteFile(filepath.Join(us.dir, name), data, 0o644); err != nil {
		return "", fmt.Errorf("write upload %s: %w", name, err)
	}
	return name, nil
}

// Path resolves a filename previously returned by Save (as extracted from a
// GET /api/chat/uploads/<name> request path) back to its on-disk location.
// It rejects any name containing a path separator or a "." / ".." segment
// so a crafted request path can never be resolved outside dir.
func (us *UploadStore) Path(name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("invalid upload name %q", name)
	}
	return filepath.Join(us.dir, name), nil
}
