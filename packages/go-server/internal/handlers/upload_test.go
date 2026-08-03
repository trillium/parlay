package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pngMagic is enough of a real PNG signature for http.DetectContentType to
// classify it as image/png, without needing a fully valid image file.
var pngMagic = []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 32))

func multipartUploadRequest(t *testing.T, filename string, data []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/chat/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestHandleUploadSavesImageAndReturnsServableURL(t *testing.T) {
	st := newTestStore(t)
	rec := httptest.NewRecorder()
	handleUpload(st)(rec, multipartUploadRequest(t, "photo.png", pngMagic))

	var got uploadResponse
	decodeBody(t, rec, &got)
	if !got.OK || got.URL == "" {
		t.Fatalf("upload response = %+v, want ok=true with a url", got)
	}
	if !strings.HasPrefix(got.URL, uploadURLPrefix) {
		t.Errorf("url = %q, want prefix %q", got.URL, uploadURLPrefix)
	}

	// The returned url must be directly servable by handleServeUpload.
	getReq := httptest.NewRequest(http.MethodGet, got.URL, nil)
	getRec := httptest.NewRecorder()
	handleServeUpload(st)(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", got.URL, getRec.Code)
	}
	if !bytes.Equal(getRec.Body.Bytes(), pngMagic) {
		t.Errorf("served bytes = %q, want %q", getRec.Body.Bytes(), pngMagic)
	}
}

func TestHandleUploadRejectsNonImage(t *testing.T) {
	st := newTestStore(t)
	rec := httptest.NewRecorder()
	handleUpload(st)(rec, multipartUploadRequest(t, "notes.txt", []byte("just plain text, not an image")))

	var got uploadResponse
	decodeBody(t, rec, &got)
	if got.OK || got.URL != "" {
		t.Errorf("upload response = %+v, want ok=false with no url", got)
	}
}

func TestHandleUploadRejectsMissingFileField(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/chat/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	rec := httptest.NewRecorder()
	handleUpload(newTestStore(t))(rec, req)

	var got uploadResponse
	decodeBody(t, rec, &got)
	if got.OK {
		t.Errorf("upload response = %+v, want ok=false", got)
	}
}

func TestHandleUploadWrongMethodIs405(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/chat/upload", nil)
	rec := httptest.NewRecorder()
	handleUpload(newTestStore(t))(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleServeUploadUnknownNameIs404(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, uploadURLPrefix+"does-not-exist.png", nil)
	rec := httptest.NewRecorder()
	handleServeUpload(newTestStore(t))(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleServeUploadRejectsTraversal(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, uploadURLPrefix+"../../../etc/passwd", nil)
	rec := httptest.NewRecorder()
	handleServeUpload(newTestStore(t))(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("status = 200 for a traversal path, want a non-200 rejection")
	}
}
