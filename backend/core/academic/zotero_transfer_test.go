package academic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"lazymind/core/common/orm"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zoteroChunkCounter struct {
	max   int
	total int64
}

func (w *zoteroChunkCounter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		w.max = len(p)
	}
	w.total += int64(len(p))
	return len(p), nil
}
func TestZoteroDatabaseTransferStreamsBoundedChunks(t *testing.T) {
	dir, _ := zoteroDatabaseFixture(t)
	path := filepath.Join(dir, "storage", "ATTACH01", "paper.pdf")
	f, err := os.OpenFile(path, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(16 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	client, err := newZoteroClient(zoteroConfig{Source: "database", LibraryType: "users", LibraryID: "0"})
	if err != nil {
		t.Fatal(err)
	}
	var sink zoteroChunkCounter
	name, err := client.copyDatabaseAttachment(t.Context(), "ATTACH01", &sink)
	if err != nil {
		t.Fatal(err)
	}
	if name != "paper.pdf" || sink.total != 16<<20 || sink.max > 64<<10 {
		t.Fatalf("not streamed: name=%s total=%d max=%d", name, sink.total, sink.max)
	}
	// Cancellation must stop copying, not finish a full disk read.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.copyDatabaseAttachment(ctx, "ATTACH01", io.Discard)
	if err == nil {
		t.Fatal("canceled transfer succeeded")
	}
}
func TestZoteroContextReaderStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	reader := zoteroContextReader{ctx: ctx, reader: nil}
	if _, err := reader.Read(make([]byte, 5)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestZoteroTransferStoresDirectlyAndReusesCommittedDocument(t *testing.T) {
	db, _ := zoteroStorageFixture(t)
	dir, _ := zoteroDatabaseFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "storage", "ATTACH01", "paper.pdf"), []byte("%PDF-1.7\nfixture\n%%EOF"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_ZOTERO_LOCAL_OWNER", "owner")
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/auth/me") {
			return zoteroResponse(r, 200, `{"user_id":"owner","username":"owner","role":"system-admin"}`), nil
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	var saved string
	for attempt := 0; attempt < 2; attempt++ {
		r := httptest.NewRequest("POST", "/academic/zotero/attachment/transfer?target_dataset_id=papers", strings.NewReader(`{"source":"database","library_type":"users","library_id":"0","item_key":"ATTACH01","paper_id":"paper","attachment_id":"zotero-attachment"}`))
		r.Header.Set("X-User-Id", "owner")
		r.Header.Set("X-User-Name", "Owner")
		r.Header.Set("Authorization", "Bearer test-owner")
		w := httptest.NewRecorder()
		TransferZoteroAttachment(w, r)
		if w.Code != 200 {
			t.Fatalf("attempt %d: %d %s", attempt, w.Code, w.Body.String())
		}
		var result map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result["document_id"] == "" || result["attachment_id"] != "zotero-attachment" || len(w.Body.Bytes()) > 256 {
			t.Fatalf("invalid compact receipt: %s", w.Body.String())
		}
		if attempt == 0 {
			saved = result["document_id"]
			if err := os.Remove(filepath.Join(dir, "storage", "ATTACH01", "paper.pdf")); err != nil {
				t.Fatal(err)
			}
		} else if saved != result["document_id"] {
			t.Fatal("retry created another document")
		}
	}
	var count int64
	if err := db.Model(&orm.Document{}).Where("dataset_id=?", "papers").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("documents=%d err=%v", count, err)
	}
}
func TestZoteroTransferRejectsHostAccessBeforeReadingFiles(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "")
	t.Setenv("LAZYMIND_ZOTERO_LOCAL_OWNER", "")
	r := httptest.NewRequest("POST", "/academic/zotero/attachment/transfer?target_dataset_id=papers", strings.NewReader(`{"source":"database","item_key":"ATTACH01","paper_id":"paper","attachment_id":"attachment"}`))
	r.Header.Set("X-User-Id", "outsider")
	w := httptest.NewRecorder()
	TransferZoteroAttachment(w, r)
	if w.Code != 403 {
		t.Fatalf("host source accessible: %d %s", w.Code, w.Body.String())
	}
}
