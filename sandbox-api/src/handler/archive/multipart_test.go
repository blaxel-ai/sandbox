package archive

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// multipartStorage is the part of S3's multipart upload the export uses.
type multipartStorage struct {
	mu        sync.Mutex
	parts     map[int][]byte
	completed []completedPart
	aborted   bool
	// reject makes the given part number fail, as storage refusing an upload.
	reject int
}

func (s *multipartStorage) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/part/"):
			var number int
			if _, err := fmt.Sscanf(r.URL.Path, "/part/%d", &number); err != nil {
				t.Errorf("unexpected part path %s", r.URL.Path)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("failed to read part %d: %v", number, err)
			}
			if len(r.TransferEncoding) > 0 {
				t.Errorf("part %d was sent chunked, which a presigned PUT refuses", number)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if number == s.reject {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			s.parts[number] = body
			w.Header().Set("ETag", fmt.Sprintf("%q", fmt.Sprintf("etag-%d", number)))
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/complete":
			var request completeRequest
			body, _ := io.ReadAll(r.Body)
			if err := xml.Unmarshal(body, &request); err != nil {
				t.Errorf("failed to parse the completion: %v", err)
			}
			s.mu.Lock()
			s.completed = request.Parts
			s.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<CompleteMultipartUploadResult><ETag>"final"</ETag></CompleteMultipartUploadResult>`))
		case r.Method == http.MethodDelete && r.URL.Path == "/abort":
			s.mu.Lock()
			s.aborted = true
			s.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// object is the archive as the storage assembled it from the parts.
func (s *multipartStorage) object() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	var assembled bytes.Buffer
	for _, part := range s.completed {
		assembled.Write(s.parts[part.PartNumber])
	}
	return assembled.Bytes()
}

func multipartOptions(server *httptest.Server, parts int, partSize int64) *MultipartUpload {
	upload := &MultipartUpload{
		PartSize:    partSize,
		CompleteURL: server.URL + "/complete",
		AbortURL:    server.URL + "/abort",
	}
	for number := 1; number <= parts; number++ {
		upload.PartURLs = append(upload.PartURLs, fmt.Sprintf("%s/part/%d", server.URL, number))
	}
	return upload
}

func TestExportUploadsAnArchiveLargerThanOnePart(t *testing.T) {
	root, lower := fakeSandbox(t)
	// Two parts' worth of content, so the archive is cut and reassembled rather
	// than sent as one part that happens to fit.
	write(t, filepath.Join(root, "data/big"), strings.Repeat("x", 6<<20), 0o644)

	storage := &multipartStorage{parts: map[int][]byte{}}
	server := httptest.NewServer(storage.handler(t))
	defer server.Close()

	options := exportOptions(t, root, lower)
	options.Multipart = multipartOptions(server, 4, minPartSize)

	result, err := Export(context.Background(), options)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if !result.Uploaded {
		t.Error("expected the export to report the upload")
	}
	if result.Size <= minPartSize {
		t.Fatalf("expected an archive larger than one part, got %d bytes", result.Size)
	}

	storage.mu.Lock()
	completed := len(storage.completed)
	storage.mu.Unlock()
	if completed != 2 {
		t.Errorf("expected the archive to be uploaded as 2 parts, got %d", completed)
	}

	object := storage.object()
	if int64(len(object)) != result.Size {
		t.Errorf("the assembled object is %d bytes, the export announced %d", len(object), result.Size)
	}
	if manifest := readManifest(t, object); manifest.Added == 0 {
		t.Error("expected the assembled object to be the archive")
	}
}

func TestExportAbortsTheMultipartUploadWhenAPartIsRejected(t *testing.T) {
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(root, "data/file"), "x", 0o644)

	storage := &multipartStorage{parts: map[int][]byte{}, reject: 1}
	server := httptest.NewServer(storage.handler(t))
	defer server.Close()

	options := exportOptions(t, root, lower)
	options.Multipart = multipartOptions(server, 2, minPartSize)

	if _, err := Export(context.Background(), options); err == nil {
		t.Fatal("expected the export to fail when a part is rejected")
	}

	storage.mu.Lock()
	defer storage.mu.Unlock()
	if !storage.aborted {
		t.Error("expected the parts already uploaded to be discarded")
	}
	if len(storage.completed) != 0 {
		t.Error("expected no object to be assembled from a failed upload")
	}
}

func TestExportRefusesTooFewPresignedParts(t *testing.T) {
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(root, "data/big"), strings.Repeat("x", 6<<20), 0o644)

	storage := &multipartStorage{parts: map[int][]byte{}}
	server := httptest.NewServer(storage.handler(t))
	defer server.Close()

	options := exportOptions(t, root, lower)
	options.Multipart = multipartOptions(server, 1, minPartSize)

	if _, err := Export(context.Background(), options); err == nil {
		t.Fatal("expected an archive needing more parts than were presigned to be refused")
	}
	// Nothing may be uploaded: a partial upload of an archive that cannot be
	// completed is the failure this check exists to avoid.
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if len(storage.parts) != 0 {
		t.Errorf("expected no part to be uploaded, got %d", len(storage.parts))
	}
}

func TestExportRefusesAnUnusableMultipartUpload(t *testing.T) {
	root, lower := fakeSandbox(t)
	options := exportOptions(t, root, lower)
	options.Multipart = &MultipartUpload{PartURLs: []string{"https://storage/part/1"}, PartSize: 1024, CompleteURL: "https://storage/complete"}

	_, err := Export(context.Background(), options)
	var invalid *InvalidOptionsError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected a part size below the storage minimum to be refused as invalid options, got %v", err)
	}
	if Quiesced() {
		t.Error("a refused export must not leave the sandbox frozen")
	}
}
