package pdfir

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archive(t *testing.T, content string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, _ := w.Create("paper_content_list.json")
	f.Write([]byte(content))
	w.Close()
	return b.Bytes()
}
func TestAdapterGeometryAndStableIDs(t *testing.T) {
	raw := archive(t, `[{"type":"equation","page_idx":0,"bbox":[100,200,900,300],"text":"$$x^2$$"},{"type":"table","page_idx":1,"bbox":[4,3,2,1]},{"type":"equation","page_idx":0,"text":"y"}]`)
	d, e := AdaptZIP(raw, "hash", "vlm")
	if e != nil {
		t.Fatal(e)
	}
	if len(d.Blocks) != 3 || d.Blocks[0].Type != "formula" || d.Blocks[0].Latex != "x^2" || d.Blocks[0].BBox[0] != 0.1 || len(d.Blocks[1].BBox) != 0 || len(d.Blocks[2].BBox) != 0 {
		t.Fatalf("bad adaptation: %+v", d)
	}
	d2, _ := AdaptZIP(raw, "hash", "vlm")
	d3, _ := AdaptZIP(raw, "changed", "vlm")
	if d.Blocks[0].ID != d2.Blocks[0].ID || d.Blocks[0].ID == d3.Blocks[0].ID {
		t.Fatal("unstable IDs or missing source identity")
	}
}
func TestAdapterRejectsMalformedArchive(t *testing.T) {
	for _, raw := range [][]byte{[]byte("bad"), archive(t, `{}`), archive(t, `[{"page_idx":-1}]`)} {
		if _, e := AdaptZIP(raw, "h", "vlm"); e == nil {
			t.Fatal("accepted malformed archive")
		}
	}
}
func TestSelectedConfigNeverFallsBackToCloud(t *testing.T) {
	for _, cfg := range []map[string]any{nil, {"ocr_type": "paddleocr"}, {"ocr_type": "mineru", "ocr_url": "http://localhost:8000"}} {
		if _, e := FromConfig(cfg); e == nil {
			t.Fatal("unavailable config accepted")
		}
	}
}
func TestSourceHashUsesBytes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.pdf")
	os.WriteFile(p, []byte("%PDF-a"), 0600)
	a, _ := SourceHash(p)
	os.WriteFile(p, []byte("%PDF-b"), 0600)
	b, _ := SourceHash(p)
	if a == b {
		t.Fatal("source replacement must invalidate cache")
	}
}
func TestClientFailureAndNoCredentialOnArtifactRequests(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/file-urls/batch":
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Error("missing API credential")
			}
			w.Write([]byte(`{"code":0,"data":{"batch_id":"b","file_urls":["` + server.URL + `/upload"]}}`))
		case "/upload":
			if r.Header.Get("Authorization") != "" {
				t.Error("credential leaked")
			}
		case "/extract-results/batch/b":
			w.Write([]byte(`{"code":0,"data":{"extract_result":[{"state":"failed","err_msg":"secret internal"}]}}`))
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	c := Client{BaseURL: server.URL, APIKey: "secret", Model: "vlm", HTTP: server.Client()}
	p := filepath.Join(t.TempDir(), "x.pdf")
	os.WriteFile(p, []byte("%PDF-a"), 0600)
	if _, e := c.Parse(context.Background(), p); e == nil || e.Error() != "MinerU parsing failed" {
		t.Fatalf("unsafe or missing error: %v", e)
	}
}

func TestClientSuccessUploadsExactBytesAndRetainsArchive(t *testing.T) {
	raw := archive(t, `[{"type":"equation","page_idx":0,"bbox":[10,20,30,40],"text":"z"}]`)
	source := []byte("%PDF-source-immutable")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/file-urls/batch":
			w.Write([]byte(`{"code":0,"data":{"batch_id":"b","file_urls":["` + server.URL + `/upload"]}}`))
		case "/upload":
			if r.ContentLength != int64(len(source)) {
				t.Errorf("upload requires known length, got %d", r.ContentLength)
			}
			b, _ := io.ReadAll(r.Body)
			if !bytes.Equal(b, source) {
				t.Error("source changed")
			}
			if r.Header.Get("Authorization") != "" {
				t.Error("upload credential leak")
			}
		case "/extract-results/batch/b":
			w.Write([]byte(`{"code":0,"data":{"extract_result":[{"state":"done","full_zip_url":"` + server.URL + `/result"}]}}`))
		case "/result":
			if r.Header.Get("Authorization") != "" {
				t.Error("download credential leak")
			}
			w.Write(raw)
		}
	}))
	defer server.Close()
	p := filepath.Join(t.TempDir(), "source.pdf")
	os.WriteFile(p, source, 0600)
	got, e := (Client{BaseURL: server.URL, APIKey: "secret", Model: "vlm", HTTP: server.Client()}).Parse(context.Background(), p)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatalf("archive altered: %v", e)
	}
}
func TestArtifactURLRejectsUntrustedHosts(t *testing.T) {
	c := Client{BaseURL: "https://mineru.net/api/v4"}
	for _, target := range []string{"http://mineru.oss-cn-shanghai.aliyuncs.com/a", "https://127.0.0.1/a", "https://evil.example/a", "https://mineru.net.evil.example/a"} {
		if _, e := c.request(context.Background(), "GET", target, nil, false); e == nil {
			t.Fatal("accepted untrusted artifact URL")
		}
	}
}

func TestOfficialCDNDownloadWithoutCredential(t *testing.T) {
	calls := 0
	c := Client{BaseURL: "https://mineru.net/api/v4", APIKey: "secret", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "cdn-mineru.openxlab.org.cn" || r.Header.Get("Authorization") != "" {
			t.Fatal("CDN request destination or credential scope is wrong")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("zip")), Header: make(http.Header)}, nil
	})}}
	if _, err := c.request(context.Background(), http.MethodGet, "https://cdn-mineru.openxlab.org.cn/pdf/result.zip", nil, false); err != nil {
		t.Fatalf("official CDN rejected: %v", err)
	}
	for _, url := range []string{"https://other.openxlab.org.cn/pdf/result.zip", "https://cdn-mineru.openxlab.org.cn.evil.example/result.zip", "http://cdn-mineru.openxlab.org.cn/result.zip"} {
		if _, err := c.request(context.Background(), http.MethodGet, url, nil, false); err == nil {
			t.Fatal("untrusted CDN alias accepted")
		}
	}
	if calls != 1 {
		t.Fatalf("untrusted request reached transport: calls=%d", calls)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
