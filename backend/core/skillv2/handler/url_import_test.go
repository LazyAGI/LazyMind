package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"lazymind/core/common"
	skillservice "lazymind/core/skillv2/service"
	"lazymind/core/skillv2/testutil"
)

func TestNormalizeSkillImportURL(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/example/skills" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
			return
		}
		const commitsPrefix = "/repos/example/skills/commits/"
		if strings.HasPrefix(r.URL.EscapedPath(), commitsPrefix) {
			ref, err := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), commitsPrefix))
			if err == nil && map[string]bool{"main": true, "feature/foo": true, "v1.2.3": true, "0123456789abcdef0123456789abcdef01234567": true}[ref] {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer apiServer.Close()

	tests := []struct {
		name       string
		rawURL     string
		wantURL    string
		wantPrefix string
		wantErr    bool
	}{
		{
			name:    "GitHub repository root",
			rawURL:  "https://github.com/example/skills",
			wantURL: "https://github.com/example/skills/archive/main.zip",
		},
		{
			name:       "GitHub tree subdirectory",
			rawURL:     "https://github.com/example/skills/tree/main/skills/target",
			wantURL:    "https://github.com/example/skills/archive/main.zip",
			wantPrefix: "skills/target",
		},
		{
			name:       "GitHub branch containing slash",
			rawURL:     "https://github.com/example/skills/tree/feature/foo/skills/target",
			wantURL:    "https://github.com/example/skills/archive/feature%2Ffoo.zip",
			wantPrefix: "skills/target",
		},
		{
			name:       "GitHub tag",
			rawURL:     "https://github.com/example/skills/tree/v1.2.3/skills/target",
			wantURL:    "https://github.com/example/skills/archive/v1.2.3.zip",
			wantPrefix: "skills/target",
		},
		{
			name:       "GitHub commit SHA",
			rawURL:     "https://github.com/example/skills/tree/0123456789abcdef0123456789abcdef01234567/skills/target",
			wantURL:    "https://github.com/example/skills/archive/0123456789abcdef0123456789abcdef01234567.zip",
			wantPrefix: "skills/target",
		},
		{
			name:    "GitHub tag archive URL",
			rawURL:  "https://github.com/example/skills/archive/refs/tags/v1.2.3.zip",
			wantURL: "https://github.com/example/skills/archive/v1.2.3.zip",
		},
		{
			name:    "GitHub branch archive URL",
			rawURL:  "https://github.com/example/skills/archive/refs/heads/main.zip",
			wantURL: "https://github.com/example/skills/archive/main.zip",
		},
		{
			name:    "GitHub branch archive URL containing slash",
			rawURL:  "https://github.com/example/skills/archive/refs/heads/feature/foo.zip",
			wantURL: "https://github.com/example/skills/archive/feature%2Ffoo.zip",
		},
		{
			name:    "direct zip URL",
			rawURL:  "https://example.test/skills.zip",
			wantURL: "https://example.test/skills.zip",
		},
		{
			name:    "SkillHub namespaced skill page",
			rawURL:  "https://skillhub.cn/skills/clawhub_paudyyin/summarize",
			wantURL: "https://api.skillhub.cn/api/v1/download?slug=%40clawhub_paudyyin%2Fsummarize",
		},
		{
			name:    "SkillHub unnamespaced skill page",
			rawURL:  "https://skillhub.cn/skills/summarize",
			wantURL: "https://api.skillhub.cn/api/v1/download?slug=summarize",
		},
		{
			name:    "invalid scheme",
			rawURL:  "ftp://example.test/skills.zip",
			wantErr: true,
		},
		{
			name:    "GitHub tree missing path",
			rawURL:  "https://github.com/example/skills/tree/main",
			wantErr: true,
		},
		{
			name:    "GitHub URL with query",
			rawURL:  "https://github.com/example/skills?download=1",
			wantErr: true,
		},
		{
			name:    "SkillHub URL missing slug",
			rawURL:  "https://skillhub.cn/skills",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotPrefix, err := normalizeSkillImportURLWithResolver(context.Background(), tt.rawURL, apiServer.Client(), apiServer.URL)
			if tt.wantErr {
				if err == nil {
					t.Fatal("normalizeSkillImportURL returned nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeSkillImportURL returned error: %v", err)
			}
			if gotURL != tt.wantURL || gotPrefix != tt.wantPrefix {
				t.Fatalf("normalizeSkillImportURL = (%q, %q), want (%q, %q)", gotURL, gotPrefix, tt.wantURL, tt.wantPrefix)
			}
		})
	}
}

type recordingZipDownloader struct {
	path   string
	gotURL string
	calls  int
}

func (d *recordingZipDownloader) Download(_ context.Context, rawURL string) (skillservice.DownloadedZip, error) {
	d.gotURL = rawURL
	d.calls++
	return skillservice.DownloadedZip{Path: d.path}, nil
}

func TestSkillHubPageURLImportResolvesDownloadAndPreservesSource(t *testing.T) {
	const sourceURL = "https://skillhub.cn/skills/clawhub_paudyyin/summarize"
	const downloadURL = "https://api.skillhub.cn/api/v1/download?slug=%40clawhub_paudyyin%2Fsummarize"

	var request createSkillRequest
	payload := []byte(`{"source":{"type":"url","url":"` + sourceURL + `"}}`)
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatalf("decode create request: %v", err)
	}
	source, cleanup, err := createSkillSourceFromRequest(
		context.Background(), request.Name, request.Category, request.Description, request.Content, request.Children, request.Source,
	)
	if err != nil {
		t.Fatalf("convert create request source: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if source.URL != downloadURL || source.SourceURL != sourceURL {
		t.Fatalf("source = %#v, want download URL %q and source URL %q", source, downloadURL, sourceURL)
	}

	zipPath, err := writeSkillPackageZip(map[string][]byte{
		"SKILL.md": []byte("---\nname: skillhub-chain-test\ndescription: Verifies the SkillHub URL import chain.\n---\n# SkillHub chain test\n"),
	})
	if err != nil {
		t.Fatalf("write skill package: %v", err)
	}
	defer os.Remove(zipPath)

	db := testutil.NewTestDB(t)
	downloader := &recordingZipDownloader{path: zipPath}
	service := skillservice.NewSkillService(skillservice.SkillServiceDeps{
		DB:         db.DB,
		Downloader: downloader,
		BlobStore:  skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(t.TempDir())),
	})
	response, err := service.CreateSkill(context.Background(), skillservice.CreateSkillRequest{
		OwnerUserID:  "user_001",
		CreateUserID: "user_001",
		Source:       source,
	})
	if err != nil {
		t.Fatalf("CreateSkill from SkillHub page URL returned error: %v", err)
	}
	if downloader.calls != 1 || downloader.gotURL != downloadURL {
		t.Fatalf("downloader calls = %d, URL = %q; want one call with %q", downloader.calls, downloader.gotURL, downloadURL)
	}

	var revision testutil.SkillRevisionRow
	if err := db.Where("id = ?", response.HeadRevisionID).Take(&revision).Error; err != nil {
		t.Fatalf("query imported skill revision: %v", err)
	}
	if revision.SourceRefType != "url" || revision.SourceRefID != sourceURL {
		t.Fatalf("source reference = (%q, %q), want (%q, %q)", revision.SourceRefType, revision.SourceRefID, "url", sourceURL)
	}
}

func TestCreateSkillFromInvalidURLReturnsInvalidParams(t *testing.T) {
	db := testutil.NewTestDB(t)
	withHandlerDB(t, db)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not a zip</html>"))
	}))
	defer server.Close()

	payload, err := json.Marshal(map[string]any{
		"source": map[string]any{
			"type": "url",
			"url":  server.URL + "/skill",
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/core/skills", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()

	Create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Create status = %d, want %d; body=%s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	var response common.APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != common.ErrCodeInvalidParams {
		t.Fatalf("response code = %d, want %d", response.Code, common.ErrCodeInvalidParams)
	}
	if got := testutil.CountRows(t, db, "skills", ""); got != 0 {
		t.Fatalf("skills count = %d, want 0", got)
	}
}

func TestHTTPZipDownloaderRejectsOversizedContentLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(maxSkillDownloadBytes+1, 10))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, err := (httpZipDownloader{}).Download(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "download exceeds") {
		t.Fatalf("error = %v, want download size error", err)
	}
}
