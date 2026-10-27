package workflow

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/staticstorage"
	corestore "lazymind/core/store"
)

func exportPNG() []byte {
	data, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl6Y28AAAAASUVORK5CYII=")
	return data
}

func TestDocumentImageExportArchiveAndPartialRetry(t *testing.T) {
	directory := t.TempDir()
	payload := imageExportPayload{Images: []imageExportSource{{ID: "image-001", Name: "First"}, {ID: "image-002", Name: "Second"}}}
	counts := map[string]int{}
	failSecond := true
	fetch := func(source imageExportSource) ([]byte, string) {
		counts[source.ID]++
		if source.ID == "image-002" && failSecond {
			return nil, "IMAGE_UNAVAILABLE"
		}
		return exportPNG(), ""
	}
	state, err := buildDocumentImageExport(t.Context(), "first", directory, payload, fetch, func(int) error { return nil })
	if err != nil || state.Ready != 1 || state.Items[1].ErrorCode != "IMAGE_UNAVAILABLE" {
		t.Fatalf("state=%#v err=%v", state, err)
	}
	checkArchive := func(job string, names []string) {
		t.Helper()
		archive, err := zip.OpenReader(filepath.Join(directory, job+".zip"))
		if err != nil {
			t.Fatal(err)
		}
		defer archive.Close()
		if len(archive.File) != len(names) {
			t.Fatalf("archive contains %d files", len(archive.File))
		}
		for index, file := range archive.File {
			if file.Name != names[index] {
				t.Fatalf("filename %q", file.Name)
			}
			reader, _ := file.Open()
			data, _ := io.ReadAll(reader)
			reader.Close()
			if !bytes.Equal(data, exportPNG()) {
				t.Fatal("original image changed")
			}
		}
	}
	checkArchive("first", []string{"001-First.png"})
	failSecond = false
	state, err = buildDocumentImageExport(t.Context(), "retry", directory, payload, fetch, func(int) error { return nil })
	if err != nil || state.Ready != 2 || counts["image-001"] != 1 || counts["image-002"] != 2 {
		t.Fatalf("state=%#v counts=%v err=%v", state, counts, err)
	}
	checkArchive("retry", []string{"001-First.png", "002-Second.png"})
	checkArchive("first", []string{"001-First.png"})
}

func TestDocumentImageExportRejectsPrivateNetworkAndDisguisedFiles(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.1.2", "::1", "::ffff:127.0.0.1", "2002:7f00:1::", "64:ff9b::7f00:1", "2001:db8::1"} {
		if imagePublicIP(netip.MustParseAddr(ip)) {
			t.Fatalf("accepted %s", ip)
		}
	}
	for _, u := range []string{"file:///etc/passwd", "ftp://example.org/a.png", "https://user:password@example.org/a.png", "http://example.org:8000/a.png"} {
		if validImageRemoteURL(u) {
			t.Fatalf("accepted %s", u)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("private server must not be contacted") }))
	defer server.Close()
	client := imageExportHTTPClient()
	defer client.CloseIdleConnections()
	_, code := fetchDocumentImage(t.Context(), client, "owner", imageExportSource{Source: server.URL + "/image"})
	if code == "" {
		t.Fatal("accepted private endpoint")
	}
	for _, body := range [][]byte{[]byte("<html>not an image</html>"), []byte("secret configuration")} {
		if imageFileExtension(body) != "" {
			t.Fatal("accepted a disguised non-image")
		}
	}
	_, code = fetchDocumentImage(t.Context(), client, "owner", imageExportSource{Source: "data:image/png;base64," + base64.StdEncoding.EncodeToString(exportPNG()), ExpectedHash: strings.Repeat("0", 64)})
	if code != "IMAGE_CHANGED" {
		t.Fatalf("hash mismatch code=%q", code)
	}
}

func TestDocumentImageExportRevalidatesRedirects(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "http://127.0.0.1/private", http.StatusFound)
	}))
	defer server.Close()
	client := imageExportHTTPClient()
	defer client.CloseIdleConnections()
	// Map a public fixture hostname to the local test server only for its first
	// request; redirected IPs still use the production SSRF-protected dialer.
	transport := client.Transport.(*http.Transport)
	originalDial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "example.org:80" {
			return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "http://"))
		}
		return originalDial(ctx, network, address)
	}
	_, code := fetchDocumentImage(t.Context(), client, "owner", imageExportSource{Source: "http://example.org/image"})
	if code == "" || calls != 1 {
		t.Fatalf("code=%q calls=%d", code, calls)
	}
}

func imageExportHTTP(t *testing.T, handler http.HandlerFunc, owner, artifactID, jobID, fileID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	request.Header.Set("X-User-Id", owner)
	request = mux.SetURLVars(request, map[string]string{"artifact_id": artifactID, "export_id": jobID, "file_id": fileID})
	response := httptest.NewRecorder()
	handler(response, request)
	return response
}

func imageExportReply(t *testing.T, response *httptest.ResponseRecorder) DocumentImageExportState {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
	}
	var reply struct {
		Data DocumentImageExportState `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	return reply.Data
}

type exportTestReporter struct{}

func (exportTestReporter) SetProgress(context.Context, int64, int64) error { return nil }
func (exportTestReporter) Heartbeat(context.Context) error                 { return nil }

func TestDocumentImageExportHTTPFreezesEditsChecksOwnershipAndDownloads(t *testing.T) {
	f, input := publicationFixture(t, false)
	if err := f.db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	corestore.Init(f.db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	inline := "data:image/png;base64," + base64.StdEncoding.EncodeToString(exportPNG())
	content, _ := json.Marshal("# Story\n![Adopted](" + inline + ")")
	body := DocumentImageExportRequest{BaseRevision: &input.BaseRevision, BaseDraftVersion: input.BaseDraftVersion, Document: content}
	created := imageExportReply(t, imageExportHTTP(t, CreateDocumentImageExportHTTP, "owner", f.currentRevisionID, "", "", body))
	repeated := imageExportReply(t, imageExportHTTP(t, CreateDocumentImageExportHTTP, "owner", f.currentRevisionID, "", "", body))
	if created.ID != repeated.ID || created.Total != 1 {
		t.Fatalf("not deduplicated %#v %#v", created, repeated)
	}
	for _, handler := range []http.HandlerFunc{ReadDocumentImageExportHTTP, RetryDocumentImageExportHTTP, DownloadDocumentImageExportHTTP} {
		response := imageExportHTTP(t, handler, "other", f.currentRevisionID, created.ID, "bundle", nil)
		if response.Code != 404 {
			t.Fatalf("cross-user access: %d", response.Code)
		}
	}
	// Subsequent edits must not change this export's adopted image list.
	if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Updates(map[string]any{"value": json.RawMessage(`{"schema":"text/markdown","data":"No pictures now"}`), "draft_version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	job, _ := asyncjob.Get(t.Context(), f.db.DB, created.ID)
	result, err := runDocumentImageExport(t.Context(), asyncjob.Job{ID: job.ID, CreateUserID: "owner", PayloadJSON: job.PayloadJSON}, exportTestReporter{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(job).Updates(map[string]any{"status": "succeeded", "result_json": result.ResultJSON}).Error; err != nil {
		t.Fatal(err)
	}
	ready := imageExportReply(t, imageExportHTTP(t, ReadDocumentImageExportHTTP, "owner", f.currentRevisionID, created.ID, "", nil))
	if ready.Status != "ready" || ready.Ready != 1 {
		t.Fatalf("not ready %#v", ready)
	}
	retried := imageExportReply(t, imageExportHTTP(t, RetryDocumentImageExportHTTP, "owner", f.currentRevisionID, created.ID, "", nil))
	if retried.ID != created.ID || retried.Status != "ready" {
		t.Fatalf("retry unnecessarily queued another archive: %#v", retried)
	}
	response := imageExportHTTP(t, DownloadDocumentImageExportHTTP, "owner", f.currentRevisionID, created.ID, "bundle", nil)
	if response.Code != 200 || !strings.Contains(response.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download %d", response.Code)
	}
	archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil || len(archive.File) != 1 || archive.File[0].Name != "001-Adopted.png" {
		t.Fatalf("unexpected archive: %v", err)
	}
	if response := imageExportHTTP(t, CreateDocumentImageExportHTTP, "owner", f.currentRevisionID, "", "", body); response.Code != 409 {
		t.Fatalf("stale draft accepted: %d", response.Code)
	}
	if staticstorage.Authorized(t.Context(), "subagent/_document_image_exports/anything", "owner") {
		t.Fatal("private cache must not be signed by static transport")
	}
}

func TestDocumentImageExportExpiryAndCanceledBuild(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := buildDocumentImageExport(ctx, "cancelled", directory, imageExportPayload{Images: []imageExportSource{{ID: "image-001"}}}, func(imageExportSource) ([]byte, string) { t.Fatal("fetch after cancellation"); return nil, "" }, func(int) error { return nil })
	if err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err := os.Stat(filepath.Join(directory, "cancelled.zip")); !os.IsNotExist(err) {
		t.Fatal("partial archive exposed")
	}
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	payload := imageExportPayload{CacheKey: strings.Repeat("a", 32), ExpiresAt: time.Now().Add(-time.Hour)}
	path := imageExportDirectory("owner", payload)
	_ = os.MkdirAll(path, 0o700)
	_ = os.WriteFile(filepath.Join(path, ".expires"), []byte("1"), 0o600)
	purgeDocumentImageExports(time.Now())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expired export retained")
	}
}

func TestDocumentImageSourcesUseCurrentArticleNotWholeLibrary(t *testing.T) {
	f, input := publicationFixture(t, false)
	corestore.Init(f.db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	imagePath := filepath.Join(root, "adopted.png")
	if err := os.WriteFile(imagePath, exportPNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _ := json.Marshal(map[string]any{"data": map[string]any{"assets": map[string]any{
		"one":    map[string]any{"media_asset_id": "one", "local_path": imagePath, "uri": "https://example.org/original.png", "meta": map[string]any{"source_reference": "assets/adopted.png", "sha256": imageExportHash(exportPNG())}},
		"unused": map[string]any{"media_asset_id": "unused", "uri": "https://example.org/unused.png"},
	}}})
	human := "library-human"
	if err := f.db.Create(&orm.WorkflowHumanArtifact{ID: human, SessionID: input.SessionID, Slot: "resolved_media_assets", Value: media, ContentType: "json", DraftVersion: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&orm.WorkflowSlotRevision{ID: "library", SessionID: input.SessionID, SlotID: "resolved_media_assets", Slot: "resolved_media_assets", Selected: true, Validity: "effective", Revision: 1, HumanArtifactID: &human}).Error; err != nil {
		t.Fatal(err)
	}
	target, err := prepareImageExport(t.Context(), "owner", f.currentRevisionID, DocumentImageExportRequest{BaseRevision: &input.BaseRevision, BaseDraftVersion: input.BaseDraftVersion})
	if err != nil {
		t.Fatal(err)
	}
	items, err := documentImageSources(t.Context(), target, []imageReference{{Source: "assets/adopted.png", Alt: "Adopted"}, {Source: "asset://one", Alt: "Same image"}})
	if err != nil || len(items) != 1 || len(items[0].References) != 2 || items[0].Source != imagePath {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	client := imageExportHTTPClient()
	defer client.CloseIdleConnections()
	data, code := fetchDocumentImage(t.Context(), client, "owner", items[0])
	if code != "" || !bytes.Equal(data, exportPNG()) {
		t.Fatalf("original image not read: %s", code)
	}
	// An editor snapshot cannot manufacture an arbitrary local-file permission.
	items, err = documentImageSources(t.Context(), target, []imageReference{{Source: imagePath + ".unbound"}, {Source: "/etc/passwd"}})
	if err != nil || items[0].Source != "" || items[1].Source != "" {
		t.Fatalf("unbound paths accepted: %#v", items)
	}
	foreign := filepath.Join(root, "tmp", "users", "other", "secret.png")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, exportPNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := doc.ReadDocumentImage(t.Context(), foreign, "owner", maxExportImageBytes); err == nil {
		t.Fatal("read another user's upload")
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	_ = os.WriteFile(outside, exportPNG(), 0o600)
	link := filepath.Join(root, "escape.png")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := doc.ReadDocumentImage(t.Context(), link, "owner", maxExportImageBytes); err == nil {
		t.Fatal("read escaped symlink")
	}
}

func TestDocumentImageExportReadsTypedMediaLibraries(t *testing.T) {
	for _, slot := range []string{"draft_document", "flat_draft_document"} {
		for _, carrier := range []string{"json", "file"} {
			t.Run(slot+"/"+carrier, func(t *testing.T) {
				f, input := publicationFixture(t, false)
				if err := f.db.AutoMigrate(&orm.AsyncJob{}); err != nil {
					t.Fatal(err)
				}
				corestore.Init(f.db.DB, nil, nil)
				t.Cleanup(func() { corestore.Init(nil, nil, nil) })
				uploadRoot, workspace := t.TempDir(), t.TempDir()
				t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)
				t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", workspace)
				imagePath := filepath.Join(workspace, "owner", "task", "media", "generated.png")
				if err := os.MkdirAll(filepath.Dir(imagePath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(imagePath, exportPNG(), 0o600); err != nil {
					t.Fatal(err)
				}
				media, err := json.Marshal(map[string]any{
					"schema":         "lazyllm.tools.writer.data_models.multimodal.MediaAssetLibrary",
					"schema_version": "0.1",
					"data": map[string]any{"assets": map[string]any{
						"generated": map[string]any{"media_asset_id": "generated", "local_path": imagePath,
							"meta": map[string]any{"sha256": imageExportHash(exportPNG())}},
					}},
				})
				if err != nil {
					t.Fatal(err)
				}
				if carrier == "file" {
					libraryPath := filepath.Join(uploadRoot, "resolved_media_assets.json")
					if err := os.WriteFile(libraryPath, media, 0o600); err != nil {
						t.Fatal(err)
					}
					media, err = json.Marshal(map[string]any{"path": libraryPath, "filename": "resolved_media_assets.json"})
					if err != nil {
						t.Fatal(err)
					}
				}
				mediaSlot := "resolved_media_assets"
				if slot == "flat_draft_document" {
					mediaSlot = "flat_resolved_media_assets"
				}
				if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", f.currentRevisionID).Update("slot_id", slot).Error; err != nil {
					t.Fatal(err)
				}
				human := "typed-media-human"
				if err := f.db.Create(&orm.WorkflowHumanArtifact{ID: human, SessionID: input.SessionID, Slot: mediaSlot, Value: media, ContentType: carrier, DraftVersion: 1}).Error; err != nil {
					t.Fatal(err)
				}
				if err := f.db.Create(&orm.WorkflowSlotRevision{ID: "typed-media", SessionID: input.SessionID, SlotID: mediaSlot, Slot: mediaSlot, Selected: true, Validity: "effective", Revision: 1, HumanArtifactID: &human}).Error; err != nil {
					t.Fatal(err)
				}
				content, _ := json.Marshal("# Story\n![Generated](<" + imagePath + ">)")
				body := DocumentImageExportRequest{BaseRevision: &input.BaseRevision, BaseDraftVersion: input.BaseDraftVersion, Document: content}
				created := imageExportReply(t, imageExportHTTP(t, CreateDocumentImageExportHTTP, "owner", f.currentRevisionID, "", "", body))
				job, err := asyncjob.Get(t.Context(), f.db.DB, created.ID)
				if err != nil {
					t.Fatal(err)
				}
				result, err := runDocumentImageExport(t.Context(), asyncjob.Job{ID: job.ID, CreateUserID: "owner", PayloadJSON: job.PayloadJSON}, exportTestReporter{})
				if err != nil {
					t.Fatal(err)
				}
				if err := f.db.Model(job).Updates(map[string]any{"status": "succeeded", "result_json": result.ResultJSON}).Error; err != nil {
					t.Fatal(err)
				}
				ready := imageExportReply(t, imageExportHTTP(t, ReadDocumentImageExportHTTP, "owner", f.currentRevisionID, created.ID, "", nil))
				if ready.Status != "ready" || ready.Ready != 1 {
					t.Fatalf("typed media library was not resolved: %#v", ready)
				}
				response := imageExportHTTP(t, DownloadDocumentImageExportHTTP, "owner", f.currentRevisionID, created.ID, "bundle", nil)
				if response.Code != http.StatusOK {
					t.Fatalf("download status: %d", response.Code)
				}
				archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
				if err != nil || len(archive.File) != 1 || archive.File[0].Name != "001-Generated.png" {
					t.Fatalf("unexpected image archive: %v", err)
				}
				reader, err := archive.File[0].Open()
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				data, err := io.ReadAll(reader)
				if err != nil || !bytes.Equal(data, exportPNG()) {
					t.Fatal("download did not preserve original image bytes")
				}
			})
		}
	}
}

func TestDocumentImageExportSingleMustBelongToSnapshot(t *testing.T) {
	f, input := publicationFixture(t, false)
	if err := f.db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	corestore.Init(f.db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })
	inline := "data:image/png;base64," + base64.StdEncoding.EncodeToString(exportPNG())
	content, _ := json.Marshal("![Only adopted image](" + inline + ")")
	body := DocumentImageExportRequest{BaseRevision: &input.BaseRevision, BaseDraftVersion: input.BaseDraftVersion, Document: content, ImageReference: "https://example.org/unrelated.png"}
	if response := imageExportHTTP(t, CreateDocumentImageExportHTTP, "owner", f.currentRevisionID, "", "", body); response.Code != 422 {
		t.Fatalf("unrelated single image accepted: %d", response.Code)
	}
	body.ImageReference = inline
	state := imageExportReply(t, imageExportHTTP(t, CreateDocumentImageExportHTTP, "owner", f.currentRevisionID, "", "", body))
	if !state.Single || state.Total != 1 {
		t.Fatalf("unexpected single export %#v", state)
	}
	body.Document = DocumentConvertSnapshot(`"# No images"`)
	body.ImageReference = ""
	if response := imageExportHTTP(t, CreateDocumentImageExportHTTP, "owner", f.currentRevisionID, "", "", body); response.Code != 422 {
		t.Fatalf("empty export accepted: %d", response.Code)
	}
}

func TestDocumentImageExportSingleMatchesEncodedURL(t *testing.T) {
	for _, tc := range []struct{ name, markdownURL, reference string }{
		{"unicode", "https://example.org/中文.png", "https://example.org/%E4%B8%AD%E6%96%87.png"},
		{"space", "<https://example.org/hello world.png>", "https://example.org/hello%20world.png"},
		{"already encoded", "https://example.org/%E4%B8%AD%E6%96%87.png", "https://example.org/%E4%B8%AD%E6%96%87.png"},
		{"signed query", "https://example.org/中文.png?token=a%2Fb+c&expires=123", "https://example.org/%E4%B8%AD%E6%96%87.png?token=a%2Fb+c&expires=123"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, input := publicationFixture(t, false)
			mustPublicationErrorNil(t, f.db.AutoMigrate(&orm.AsyncJob{}))
			corestore.Init(f.db.DB, nil, nil)
			t.Cleanup(func() { corestore.Init(nil, nil, nil) })
			content, _ := json.Marshal("![Photo](" + tc.markdownURL + ")")
			body := DocumentImageExportRequest{BaseRevision: &input.BaseRevision, BaseDraftVersion: input.BaseDraftVersion, Document: content, ImageReference: tc.reference}
			state := imageExportReply(t, imageExportHTTP(t, CreateDocumentImageExportHTTP, "owner", f.currentRevisionID, "", "", body))
			if !state.Single || state.Total != 1 {
				t.Fatalf("unexpected single export %#v", state)
			}
			body.ImageReference += "&other=1"
			if response := imageExportHTTP(t, CreateDocumentImageExportHTTP, "owner", f.currentRevisionID, "", "", body); response.Code != 422 {
				t.Fatalf("different resource accepted: %d", response.Code)
			}
		})
	}
	if imageReferenceKey("https://example.org/a%2Fb.png") == imageReferenceKey("https://example.org/a/b.png") {
		t.Fatal("encoded path separator must not match another resource")
	}
}

func TestDocumentImageExportConcurrentAdmissionAndExpiry(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{})
	payload := imageExportPayload{CacheKey: strings.Repeat("a", 32), ExpiresAt: time.Now().Add(time.Hour), Images: []imageExportSource{{ID: "image-001"}}}
	start := make(chan struct{})
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := enqueueDocumentImageExport(t.Context(), db.DB, asyncjob.EnqueueRequest{JobType: documentImageJobType, IdempotencyKey: fmt.Sprintf("unique-%d", i), CreateUserID: "owner", Payload: payload})
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, limited := 0, 0
	for err := range results {
		var failure documentActionFailure
		if err == nil {
			accepted++
		} else if errors.As(err, &failure) && failure.code == "IMAGE_EXPORT_BUSY" {
			limited++
		} else {
			t.Fatalf("unexpected admission error: %v", err)
		}
	}
	if accepted != 3 || limited != 5 {
		t.Fatalf("accepted=%d limited=%d", accepted, limited)
	}
	var previous orm.AsyncJob
	if err := db.Where("job_type = ?", documentImageJobType).First(&previous).Error; err != nil {
		t.Fatal(err)
	}
	request := asyncjob.EnqueueRequest{JobType: documentImageJobType, IdempotencyKey: previous.IdempotencyKey, CreateUserID: "owner", Payload: payload}
	reused, err := enqueueDocumentImageExport(t.Context(), db.DB, request)
	if err != nil || reused.ID != previous.ID {
		t.Fatalf("active job not reused: %v", err)
	}
	expired := payload
	expired.ExpiresAt = time.Now().Add(-time.Hour)
	raw, _ := json.Marshal(expired)
	if err := db.Model(&previous).Update("payload_json", raw).Error; err != nil {
		t.Fatal(err)
	}
	renewed, err := enqueueDocumentImageExport(t.Context(), db.DB, request)
	if err != nil || renewed.ID == previous.ID {
		t.Fatalf("expired job not renewed: %v", err)
	}
}
