package doc

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"io"
	"lazymind/core/common/orm"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOriginalRemoteAddressAndRedirectBoundaries(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "http://user:password@example.com/a.pdf", "http://127.0.0.1/a.pdf", "http://[::ffff:127.0.0.1]/a.pdf", "http://100.64.0.1/a.pdf", "http://169.254.169.254/a.pdf"} {
		if _, err := validateOriginalURL(raw); err == nil {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
	for _, address := range []string{"::1", "192.168.1.1", "198.18.0.1", "240.0.0.1", "2001:db8::1"} {
		if publicOriginalIP(net.ParseIP(address)) {
			t.Fatalf("nonpublic address accepted: %s", address)
		}
	}
	client := newOriginalHTTPClient()
	defer client.CloseIdleConnections()
	private, _ := url.Parse("http://127.0.0.1/x.pdf")
	if err := client.CheckRedirect(&http.Request{URL: private}, nil); err == nil {
		t.Fatal("private redirect accepted")
	}
	public, _ := url.Parse("https://example.com/x.pdf")
	if err := client.CheckRedirect(&http.Request{URL: public}, make([]*http.Request, 5)); err == nil {
		t.Fatal("redirect loop accepted")
	}
	tr := client.Transport.(*http.Transport)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if conn, err := tr.DialContext(ctx, "tcp", "localhost:80"); err == nil {
		conn.Close()
		t.Fatal("private DNS target dialed")
	}
}
func TestOriginalRemoteRejectsTruncatedOversizedAndLoginResponses(t *testing.T) {
	db := newDocumentTestDB(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	svc, _ := NewOriginalService(OriginalServiceDeps{DB: db.DB})
	caller := DatasetCatalogCaller{UserID: "owner"}
	u, _ := url.Parse("https://example.com/paper.pdf")
	for _, test := range []struct {
		name, body, mime string
		size             int64
		status           int
	}{
		{"oversize", "", "application/pdf", originalMaxBytes + 1, 200},
		{"truncated", "%PDF-1.4\n%%EOF", "application/pdf", 100, 200},
		{"login", "<html>login required</html>", "text/html", -1, 200},
		{"partial", "%PDF-1.4\n%%EOF", "application/pdf", -1, 206},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: test.status, ContentLength: test.size, Header: http.Header{"Content-Type": []string{test.mime}}, Body: io.NopCloser(strings.NewReader(test.body)), Request: &http.Request{URL: u}}
			if _, err := svc.acquireResponse(context.Background(), caller, AcquireRemoteOriginalRequest{URL: u.String(), DisplayName: "paper.pdf", ExpectedMIME: "application/pdf"}, resp); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
	var count int64
	db.Model(&orm.UploadedFile{}).Where("deleted_at IS NULL").Count(&count)
	if count != 0 {
		t.Fatalf("invalid original persisted: %d", count)
	}
}
func TestOriginalBindingUsesCallerTransactionAndRollsBack(t *testing.T) {
	db := newDocumentTestDB(t)
	installOriginalSourceTransport(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	if err := db.AutoMigrate(&orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "target", "target", "general_algo", "owner", time.Now())
	svc, _ := NewOriginalService(OriginalServiceDeps{DB: db.DB})
	caller := DatasetCatalogCaller{UserID: "owner"}
	ref, err := svc.AcquireOriginal(context.Background(), caller, AcquireOriginalRequest{Filename: "x.txt", Reader: strings.NewReader("original")})
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.PrepareOriginalBinding(context.Background(), caller, ImportOriginalRequest{FileRef: ref.FileRef, DatasetID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	authCtx, err := PrepareResourceAuthorization(context.Background(), caller, p.AuthorizationRequests())
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB.DB()
	sqlDB.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(authCtx, 2*time.Second)
	defer cancel()
	sentinel := errors.New("rollback root")
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result, err := svc.BindOriginalTx(ctx, tx, caller, p)
		if err != nil {
			return err
		}
		if result.Document.ID == "" {
			return errors.New("missing document")
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("transaction failed: %v", err)
	}
	var count int64
	db.Model(&orm.Document{}).Count(&count)
	if count != 0 {
		t.Fatalf("rollback leaked %d documents", count)
	}
	if err := svc.CleanupPreparedBinding(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.directory); !os.IsNotExist(err) {
		t.Fatal("staged copy retained")
	}
}
func TestOriginalCancellation(t *testing.T) {
	db := newDocumentTestDB(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	svc, _ := NewOriginalService(OriginalServiceDeps{DB: db.DB})
	caller := DatasetCatalogCaller{UserID: "owner"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.AcquireOriginal(ctx, caller, AcquireOriginalRequest{Filename: "x.txt", Reader: strings.NewReader("hello")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestOriginalWorkerReadAndReleaseRespectOwnershipAndBinding(t *testing.T) {
	db := newDocumentTestDB(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	svc, _ := NewOriginalService(OriginalServiceDeps{DB: db.DB})
	owner := DatasetCatalogCaller{UserID: "owner"}
	foreign := DatasetCatalogCaller{UserID: "foreign"}
	ref, err := svc.AcquireOriginal(t.Context(), owner, AcquireOriginalRequest{Filename: "paper.txt", Reader: strings.NewReader("original bytes")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.OpenOriginal(t.Context(), foreign, ref.FileRef); err == nil {
		t.Fatal("foreign user read original")
	}
	if err = svc.ReleaseOriginal(t.Context(), foreign, ref.FileRef); err == nil {
		t.Fatal("foreign user deleted original")
	}
	file, err := svc.OpenOriginal(t.Context(), owner, ref.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	db.Model(&orm.UploadedFile{}).Where("upload_file_id=?", ref.FileRef).Updates(map[string]any{"document_id": "bound", "status": UploadedFileStateBound})
	if err = svc.ReleaseOriginal(t.Context(), owner, ref.FileRef); err == nil {
		t.Fatal("released bound original")
	}
	db.Model(&orm.UploadedFile{}).Where("upload_file_id=?", ref.FileRef).Updates(map[string]any{"document_id": "", "status": UploadedFileStateUploaded})
	if err = svc.ReleaseOriginal(t.Context(), owner, ref.FileRef); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.OpenOriginal(t.Context(), owner, ref.FileRef); err == nil {
		t.Fatal("released original remained readable")
	}
}
func TestRemoteOriginalHonorsPaperSizeLimitAndCleansRejectedBytes(t *testing.T) {
	db := newDocumentTestDB(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	svc, _ := NewOriginalService(OriginalServiceDeps{DB: db.DB})
	u, _ := url.Parse("https://example.com/paper.pdf")
	for _, length := range []int64{100, -1} {
		response := &http.Response{StatusCode: 200, ContentLength: length, Header: http.Header{"Content-Type": []string{"application/pdf"}}, Body: io.NopCloser(strings.NewReader("%PDF-1.7\nlong paper bytes\n%%EOF")), Request: &http.Request{URL: u}}
		if _, err := svc.acquireResponse(t.Context(), DatasetCatalogCaller{UserID: "owner"}, AcquireRemoteOriginalRequest{MaxBytes: 16, ExpectedMIME: "application/pdf", DisplayName: "paper.pdf"}, response); err == nil {
			t.Fatal("paper limit ignored")
		}
	}
	var count int64
	db.Model(&orm.UploadedFile{}).Count(&count)
	if count != 0 {
		t.Fatal("rejected acquisition left an original", count)
	}
}

func TestAuthenticatedOriginalDoesNotPersistPrivateQuery(t *testing.T) {
	db := newDocumentTestDB(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	svc, _ := NewOriginalService(OriginalServiceDeps{DB: db.DB})
	request := AcquireRemoteOriginalRequest{URL: "https://example.com/paper.txt", PrivateQuery: map[string]string{"api_key": "private-test-key"}, DisplayName: "paper.txt", ExpectedMIME: "text/plain"}
	raw, err := json.Marshal(request)
	if err != nil || strings.Contains(string(raw), "private-test-key") {
		t.Fatal("private request fields were serialized")
	}
	target, _ := url.Parse("https://example.com/paper.txt?api_key=private-test-key&version=2")
	response := &http.Response{StatusCode: 200, ContentLength: 4, Body: io.NopCloser(strings.NewReader("text")), Header: http.Header{"Content-Type": []string{"text/plain"}}, Request: &http.Request{URL: target}}
	ref, err := svc.acquireResponse(t.Context(), DatasetCatalogCaller{UserID: "owner"}, request, response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ref.FinalURL, "api_key") || !strings.Contains(ref.FinalURL, "version=2") {
		t.Fatal("private query leaked into original reference")
	}
	var row orm.UploadedFile
	if err = db.Where("upload_file_id=?", ref.FileRef).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	persisted, _ := json.Marshal(row)
	if strings.Contains(string(persisted), "private-test-key") {
		t.Fatal("credential persisted in original metadata")
	}
}
