package systemdeps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPDFFontDownloadConcurrentOfflineAndRepair(t *testing.T) {
	payload := []byte("fixture TTF bytes")
	var requests atomic.Int32
	var corrupt atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if corrupt.Load() {
			w.Write([]byte("invalid"))
			return
		}
		w.Write(payload)
	}))
	defer server.Close()
	entry := pdfFontDescriptor{SchemaVersion: 1, Filename: "font.ttf", URL: server.URL, SizeBytes: int64(len(payload)), SHA256: fmt.Sprintf("%x", sha256.Sum256(payload))}
	root := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := ensurePDFFont(context.Background(), root, entry, server.Client()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatalf("downloaded %d times", requests.Load())
	}
	target := filepath.Join(root, "deps", "pdf-font", entry.SHA256, entry.Filename)
	os.WriteFile(target, []byte("corrupt cache"), 0600)
	corrupt.Store(true)
	if _, err := ensurePDFFont(context.Background(), root, entry, server.Client()); err == nil {
		t.Fatal("bad font accepted")
	}
	corrupt.Store(false)
	if _, err := ensurePDFFont(context.Background(), root, entry, server.Client()); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if _, err := ensurePDFFont(context.Background(), root, entry, server.Client()); err != nil {
		t.Fatalf("offline reuse: %v", err)
	}
}

func TestPDFFontCancellationAnd404Retry(t *testing.T) {
	var block atomic.Bool
	block.Store(true)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if block.Load() {
			<-r.Context().Done()
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	entry := pdfFontDescriptor{Filename: "font.ttf", URL: server.URL, SizeBytes: 3, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("abc")))}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := ensurePDFFont(ctx, root, entry, server.Client()); err == nil {
		t.Fatal("expected cancellation")
	}
	block.Store(false)
	if _, err := ensurePDFFont(context.Background(), root, entry, server.Client()); err == nil {
		t.Fatal("accepted 404")
	}
	matches, _ := filepath.Glob(filepath.Join(root, "deps", "pdf-font", entry.SHA256, ".download-*"))
	if len(matches) != 0 {
		t.Fatalf("partial downloads remain: %v", matches)
	}
}

func TestPDFFontRejectsNonLocalAndUnsafeCatalog(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "cloud")
	response := httptest.NewRecorder()
	GetPDFFont(response, httptest.NewRequest("GET", "/", nil))
	if response.Code != http.StatusForbidden {
		t.Fatal(response.Code)
	}
	catalog := filepath.Join(t.TempDir(), "font.json")
	t.Setenv("LAZYMIND_PDF_FONT_CATALOG", catalog)
	for _, data := range []string{`{"schemaVersion":1,"filename":"../bad.ttf"}`, `{"schemaVersion":1,"filename":"font.ttf","url":"http://localhost","sha256":"bad"}`} {
		os.WriteFile(catalog, []byte(data), 0600)
		if _, err := loadPDFFontDescriptor(); err == nil {
			t.Fatal("unsafe catalog accepted")
		}
	}
	req := httptest.NewRequest("GET", "http://example.test/font", nil)
	if pdfFontClient.CheckRedirect(req, nil) == nil {
		t.Fatal("HTTP redirect accepted")
	}
}

func TestPDFFontRealTTFThroughHandler(t *testing.T) {
	raw, err := os.ReadFile("../../../frontend/public/fonts/NotoSansSC-wght.ttf")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 4 || string(raw[:4]) != "\x00\x01\x00\x00" {
		t.Fatal("fixture is not TrueType")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	defer server.Close()
	entry := pdfFontDescriptor{SchemaVersion: 1, Filename: "font.ttf", URL: server.URL, SizeBytes: int64(len(raw)), SHA256: fmt.Sprintf("%x", sha256.Sum256(raw))}
	root := t.TempDir()
	catalog := filepath.Join(root, "font.json")
	data, _ := json.Marshal(entry)
	os.WriteFile(catalog, data, 0600)
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv("LAZYMIND_RUNTIME_ROOT", root)
	t.Setenv("LAZYMIND_PDF_FONT_CATALOG", catalog)
	old := pdfFontClient
	pdfFontClient = server.Client()
	t.Cleanup(func() { pdfFontClient = old })
	response := httptest.NewRecorder()
	GetPDFFont(response, httptest.NewRequest("GET", "/system-dependencies/pdf-font", nil))
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), raw) {
		t.Fatalf("font response: %d, %d bytes", response.Code, response.Body.Len())
	}
	server.Close()
	response = httptest.NewRecorder()
	GetPDFFont(response, httptest.NewRequest("GET", "/system-dependencies/pdf-font", nil))
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), raw) {
		t.Fatal("handler offline reuse failed")
	}
}
