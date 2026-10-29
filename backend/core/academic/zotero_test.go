package academic

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func zoteroTestClient(t *testing.T, config zoteroConfig, handler func(*http.Request) (*http.Response, error)) *zoteroClient {
	t.Helper()
	client, err := newZoteroClient(config)
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = importErrorTransport(handler)
	return client
}

func zoteroResponse(r *http.Request, code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Last-Modified-Version": {"9"}, "Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestZoteroPreviewPaginatesAndPreservesCollections(t *testing.T) {
	var starts []string
	client := zoteroTestClient(t, zoteroConfig{Source: "remote", LibraryType: "users", LibraryID: "123", APIKey: "private-key"}, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.zotero.org" || r.Header.Get("Zotero-API-Key") != "private-key" || r.Header.Get("Zotero-API-Version") != "3" {
			t.Fatalf("invalid source request: %s", r.URL)
		}
		if strings.HasSuffix(r.URL.Path, "/collections") {
			return zoteroResponse(r, 200, `[{"key":"PARENT01","data":{"key":"PARENT01","name":"Parent","parentCollection":false}},{"key":"CHILD001","data":{"key":"CHILD001","name":"Child","parentCollection":"PARENT01"}}]`), nil
		}
		starts = append(starts, r.URL.Query().Get("start"))
		if r.URL.Query().Get("start") == "100" {
			return zoteroResponse(r, 200, `[{"key":"LAST0001","data":{"itemType":"journalArticle","title":"Last","collections":["CHILD001"]}}]`), nil
		}
		items := make([]map[string]any, 100)
		for i := range items {
			items[i] = map[string]any{"key": fmt.Sprintf("ITEM%04d", i), "data": map[string]any{"itemType": "journalArticle", "title": "Paper"}}
		}
		b, _ := json.Marshal(items)
		return zoteroResponse(r, 200, string(b)), nil
	})
	result, err := client.preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 101 || len(result.Collections) != 2 || strings.Join(starts, ",") != "0,100" || result.SourceID != "users:123" {
		t.Fatalf("lost library: %#v starts=%v", result, starts)
	}
}

func TestZoteroPreviewRejectsChangedLibraryAndSurfacesProviderErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		changed bool
		want    int
	}{{"changed", 200, true, 409}, {"credentials", 403, false, 403}, {"rate limit", 429, false, 429}, {"upstream down", 503, false, 502}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := zoteroTestClient(t, zoteroConfig{Source: "remote", LibraryID: "123", APIKey: "do-not-leak"}, func(r *http.Request) (*http.Response, error) {
				calls++
				resp := zoteroResponse(r, tc.status, `[]`)
				if tc.changed && calls > 1 {
					resp.Header.Set("Last-Modified-Version", "10")
				}
				resp.Header.Set("Retry-After", "60")
				return resp, nil
			})
			_, err := client.preview(context.Background())
			failure, ok := err.(*zoteroError)
			if !ok || failure.Status != tc.want || strings.Contains(err.Error(), "do-not-leak") {
				t.Fatalf("error not surfaced safely: %#v", err)
			}
		})
	}
}

func TestZoteroConfigurationRejectsArbitraryLocalTargets(t *testing.T) {
	for _, local := range []string{"http://169.254.169.254:23119/api", "http://localhost:8090/api", "https://evil.example/api", "http://user:pass@localhost:23119/api", "http://localhost:23119/api?target=other", "http://localhost:23119/private"} {
		if _, err := newZoteroClient(zoteroConfig{Source: "local", LocalURL: local}); err == nil {
			t.Errorf("accepted local target %s", local)
		}
	}
	for _, local := range []string{"", "http://localhost:23119/api/", "http://127.0.0.1:23119/api", "http://host.docker.internal:23119/api"} {
		if _, err := newZoteroClient(zoteroConfig{Source: "local", LocalURL: local}); err != nil {
			t.Errorf("rejected local target %s: %v", local, err)
		}
	}
	for _, ip := range []string{"169.254.169.254", "fd00:ec2::254", "fe80::1", "0.0.0.0", "::", "224.0.0.1", "100.100.100.200", "::ffff:169.254.1.2"} {
		if zoteroAllowedIP(net.ParseIP(ip), true) {
			t.Errorf("accepted unsafe address %s", ip)
		}
	}
	if !zoteroAllowedIP(net.ParseIP("192.168.1.2"), true) || zoteroAllowedIP(net.ParseIP("192.168.1.2"), false) {
		t.Fatal("NAS/public boundaries incorrect")
	}
}

func TestZoteroLocalPreviewUsesUserZeroAndNeverSendsRemoteKey(t *testing.T) {
	client := zoteroTestClient(t, zoteroConfig{Source: "local", APIKey: "remote-secret"}, func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.URL.Path, "/api/users/0/") || r.Header.Get("Zotero-API-Key") != "" {
			t.Fatal("local identity or credential handling incorrect")
		}
		resp := zoteroResponse(r, 200, `[]`)
		resp.Header.Set("Zotero-Server-ID", "local-library")
		return resp, nil
	})
	result, err := client.preview(context.Background())
	if err != nil || !strings.Contains(result.SourceID, "local-library") || len(result.Warnings) == 0 {
		t.Fatalf("local identity/warning missing: %#v %v", result, err)
	}
}

func TestZoteroLocalDockerConnectionUsesLoopbackHost(t *testing.T) {
	for _, tc := range []struct {
		name     string
		source   string
		host     string
		wantHost string
		absolute bool
	}{
		{"docker relative redirect", "local", "host.docker.internal:23119", "localhost:23119", false},
		{"docker absolute redirect", "local", "host.docker.internal:23119", "localhost:23119", true},
		{"direct connection", "local", "127.0.0.1:23119", "127.0.0.1:23119", false},
		{"WebDAV host unchanged", "webdav", "host.docker.internal:23119", "host.docker.internal:23119", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				// Zotero's HTTP server rejects Docker's network alias as a Host.
				if r.Host != tc.wantHost {
					http.Error(w, "Bad request", http.StatusBadRequest)
					return
				}
				if tc.source == "local" && (r.Header.Get("Zotero-API-Key") != "" || r.Header.Get("Authorization") != "") {
					t.Error("remote credentials sent to local Zotero")
				}
				if r.URL.Query().Get("redirected") == "" {
					location := r.URL.Path + "?redirected=1"
					if tc.absolute {
						location = "http://" + tc.host + location
					}
					http.Redirect(w, r, location, http.StatusFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[]`))
			}))
			defer server.Close()
			config := zoteroConfig{Source: tc.source, LibraryID: "123", LocalURL: "http://" + tc.host + "/api", APIKey: "remote-secret"}
			if tc.source == "webdav" {
				config.WebDAV = &zoteroWebDAVConfig{URL: "http://" + tc.host + "/zotero", Username: "user", Password: "secret"}
			}
			client, err := newZoteroClient(config)
			if err != nil {
				t.Fatal(err)
			}
			routing := client.http.Transport.(*zoteroRoutingTransport)
			defer routing.CloseIdleConnections()
			routing.private.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != tc.host {
					t.Errorf("dial target changed: got %s, want %s", address, tc.host)
				}
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			client.http.Transport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
				originalHost := r.Host
				response, err := routing.RoundTrip(r)
				if r.Host != originalHost {
					t.Error("transport mutated the caller's request")
				}
				return response, err
			})
			target := client.apiURL("collections")
			if tc.source == "webdav" {
				target = client.dav
			}
			body, _, err := client.request(context.Background(), target, 1024)
			if err != nil || string(body) != `[]` || requests != 2 {
				t.Fatalf("local HTTP host rejected: body=%s requests=%d error=%v", body, requests, err)
			}
			if target.Host != tc.host {
				t.Fatal("request rewrote the destination URL")
			}
		})
	}
}

func TestZoteroPDFDownloadStripsCredentialsAtStorageRedirect(t *testing.T) {
	pdf := "%PDF-1.7\nlocal bytes"
	hash := md5.Sum([]byte(pdf))
	client := zoteroTestClient(t, zoteroConfig{Source: "remote", LibraryID: "123", APIKey: "private-key"}, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "storage.example" {
			if r.Header.Get("Zotero-API-Key") != "" || r.Header.Get("Authorization") != "" {
				t.Fatal("credential leaked across redirect")
			}
			return zoteroResponse(r, 200, pdf), nil
		}
		if strings.HasSuffix(r.URL.Path, "/file") {
			resp := zoteroResponse(r, 302, "")
			resp.Header.Set("Location", "https://storage.example/paper.pdf")
			return resp, nil
		}
		return zoteroResponse(r, 200, fmt.Sprintf(`{"key":"ABC12345","data":{"itemType":"attachment","contentType":"application/pdf","filename":"paper.pdf","md5":%q}}`, hex.EncodeToString(hash[:]))), nil
	})
	file, err := client.attachment(context.Background(), "ABC12345")
	if err != nil || string(file.Data) != pdf || file.Name != "paper.pdf" {
		t.Fatalf("download failed: %#v %v", file, err)
	}
}

func TestZoteroLocalFileRedirectRequiresExplicitFileSelection(t *testing.T) {
	client := zoteroTestClient(t, zoteroConfig{Source: "local"}, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/file") {
			resp := zoteroResponse(r, 302, "")
			resp.Header.Set("Location", "file:///etc/passwd")
			return resp, nil
		}
		return zoteroResponse(r, 200, `{"key":"ABC12345","data":{"itemType":"attachment","contentType":"application/pdf","filename":"paper.pdf"}}`), nil
	})
	_, err := client.attachment(context.Background(), "ABC12345")
	failure, ok := err.(*zoteroError)
	if !ok || failure.Code != "LOCAL_ATTACHMENT_REQUIRED" || strings.Contains(err.Error(), "/etc/passwd") {
		t.Fatalf("unsafe or hidden local redirect: %#v", err)
	}
}

func zoteroZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, body := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestZoteroWebDAVImportsVerifiedPDFAndRejectsUnsafeArchives(t *testing.T) {
	for _, tc := range []struct {
		name      string
		files     map[string]string
		wantError bool
	}{
		{"pdf", map[string]string{"paper.pdf": "%PDF-1.7\ncontent"}, false},
		{"legacy encoded PDF", map[string]string{base64.StdEncoding.EncodeToString([]byte("paper.pdf")) + "%ZB64": "%PDF-1.7\ncontent"}, false},
		{"legacy encoded traversal", map[string]string{base64.StdEncoding.EncodeToString([]byte("../paper.pdf")) + "%ZB64": "%PDF-1.7\ncontent"}, true},
		{"traversal", map[string]string{"../paper.pdf": "%PDF-1.7\ncontent"}, true},
		{"wrong file", map[string]string{"other.pdf": "%PDF-1.7\ncontent"}, true},
		{"html as pdf", map[string]string{"paper.pdf": "<html>login</html>"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := zoteroZIP(t, tc.files)
			client := zoteroTestClient(t, zoteroConfig{Source: "webdav", LibraryID: "123", APIKey: "zotero-key", WebDAV: &zoteroWebDAVConfig{URL: "https://dav.example/files", Username: "user", Password: "secret"}}, func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "dav.example" {
					u, p, ok := r.BasicAuth()
					if !ok || u != "user" || p != "secret" || r.Header.Get("Zotero-API-Key") != "" || r.URL.Path != "/files/zotero/ABC12345.zip" {
						t.Fatalf("incorrect WebDAV request: %s", r.URL)
					}
					resp := zoteroResponse(r, 200, string(archive))
					return resp, nil
				}
				return zoteroResponse(r, 200, `{"key":"ABC12345","data":{"itemType":"attachment","contentType":"application/pdf","filename":"paper.pdf"}}`), nil
			})
			_, err := client.attachment(context.Background(), "ABC12345")
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected archive result: %v", err)
			}
		})
	}
}

func TestZoteroHandlersRequireTargetWritePermissionBeforeNetworking(t *testing.T) {
	paperLibraryTestDB(t)
	for _, handler := range []http.HandlerFunc{PreviewZoteroLibrary, DownloadZoteroAttachment} {
		for _, user := range []string{"", "outsider"} {
			r := httptest.NewRequest("POST", "/academic/zotero/preview?target_dataset_id=papers", strings.NewReader(`{"source":"remote","library_id":"123"}`))
			r.Header.Set("X-User-Id", user)
			w := httptest.NewRecorder()
			handler(w, r)
			if w.Code != 400 && w.Code != 403 {
				t.Fatalf("unauthorized importer: %d %s", w.Code, w.Body.String())
			}
		}
	}
}

func TestZoteroRejectsInvalidPayloadAndCanceledRequests(t *testing.T) {
	for _, body := range []string{`{"error":"not an item list"}`, `null`, `[{"key":"BAD","data":{"itemType":"journalArticle"}}]`, `[{"key":"ABC12345"},{"key":"ABC12345"}]`} {
		client := zoteroTestClient(t, zoteroConfig{Source: "remote", LibraryID: "123"}, func(r *http.Request) (*http.Response, error) { return zoteroResponse(r, 200, body), nil })
		if _, err := client.preview(context.Background()); err == nil {
			t.Errorf("accepted invalid library %s", body)
		}
	}
	client := zoteroTestClient(t, zoteroConfig{Source: "remote", LibraryID: "123", APIKey: "secret-key"}, func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("network failure with %s", r.Header.Get("Zotero-API-Key"))
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.preview(ctx)
	failure, ok := err.(*zoteroError)
	if !ok || failure.Status != 504 || strings.Contains(failure.Message, "secret-key") {
		t.Fatalf("unsafe canceled request: %#v", err)
	}
}

func TestZoteroWebDAVCredentialsNeverFollowCrossOriginRedirect(t *testing.T) {
	client := zoteroTestClient(t, zoteroConfig{Source: "webdav", LibraryID: "123", WebDAV: &zoteroWebDAVConfig{URL: "https://dav.example/zotero", Username: "user", Password: "secret"}}, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "evil.example" {
			t.Fatal("cross-origin WebDAV request was sent")
		}
		if r.URL.Host == "dav.example" {
			resp := zoteroResponse(r, 302, "")
			resp.Header.Set("Location", "https://evil.example/capture")
			return resp, nil
		}
		return zoteroResponse(r, 200, `{"key":"ABC12345","data":{"itemType":"attachment","contentType":"application/pdf","filename":"paper.pdf"}}`), nil
	})
	_, err := client.attachment(context.Background(), "ABC12345")
	failure, ok := err.(*zoteroError)
	if !ok || failure.Code != "ZOTERO_REDIRECT_REJECTED" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe redirect: %#v", err)
	}
}

func TestZoteroAttachmentHashAndSizeFailuresAreNotReportedAsSuccess(t *testing.T) {
	for _, tc := range []struct {
		name      string
		oversized bool
		want      string
	}{{"changed hash", false, "ZOTERO_ATTACHMENT_CHANGED"}, {"oversized", true, "ZOTERO_RESPONSE_TOO_LARGE"}} {
		t.Run(tc.name, func(t *testing.T) {
			client := zoteroTestClient(t, zoteroConfig{Source: "remote", LibraryID: "123"}, func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/file") {
					resp := zoteroResponse(r, 200, "%PDF-1.7\ncontent")
					if tc.oversized {
						resp.ContentLength = zoteroMaxPDFBytes + 1
					}
					return resp, nil
				}
				return zoteroResponse(r, 200, `{"key":"ABC12345","data":{"itemType":"attachment","contentType":"application/pdf","filename":"paper.pdf","md5":"00000000000000000000000000000000"}}`), nil
			})
			_, err := client.attachment(context.Background(), "ABC12345")
			failure, ok := err.(*zoteroError)
			if !ok || failure.Code != tc.want {
				t.Fatalf("attachment failure was not visible: %#v", err)
			}
		})
	}
}

func TestZoteroZIPRejectsOversizedExpandedFiles(t *testing.T) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	header := &zip.FileHeader{Name: "paper.pdf", Method: zip.Store, UncompressedSize64: zoteroMaxPDFBytes + 1}
	if _, err := w.CreateRaw(header); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := readZoteroZIP(b.Bytes(), "paper.pdf")
	failure, ok := err.(*zoteroError)
	if !ok || failure.Code != "ZOTERO_ARCHIVE_TOO_LARGE" {
		t.Fatalf("ZIP expansion limit missing: %#v", err)
	}
}

func TestZoteroPreviewDoesNotAcceptIncompletePagination(t *testing.T) {
	client := zoteroTestClient(t, zoteroConfig{Source: "remote", LibraryID: "123"}, func(r *http.Request) (*http.Response, error) {
		resp := zoteroResponse(r, 200, `[]`)
		resp.Header.Set("Total-Results", "2")
		return resp, nil
	})
	if _, err := client.preview(context.Background()); err == nil {
		t.Fatal("partial response was reported as a complete library")
	}
}
