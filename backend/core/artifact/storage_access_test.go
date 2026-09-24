package artifact

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/staticstorage"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignStaticFilesRejectsRepeatedEncoding(t *testing.T) {
	svc := New(v2TestDB(t).DB)
	t.Setenv("LAZYMIND_FILE_URL_SIGN_SECRET", "encoding-regression-secret")
	store.Init(svc.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	view, err := svc.CommitRevision(context.Background(), CommitRequest{
		OwnerUserID: "victim", ContentType: "file", Content: []byte("private bytes"),
		Bindings: []BindingSpec{{ScopeType: ScopeConversation, ScopeID: "private"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	signed, _, err := SignRevisionURL(context.Background(), svc, "victim", view.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.TrimPrefix(strings.SplitN(signed, "?", 2)[0], "/static-files/artifacts/")
	sign := func(user, path string) string {
		t.Helper()
		body, _ := json.Marshal(signStaticFilesRequest{Paths: []string{path}})
		req := httptest.NewRequest(http.MethodPost, "/static-files:sign", strings.NewReader(string(body)))
		req.Header.Set("X-User-Id", user)
		w := httptest.NewRecorder()
		doc.SignStaticFiles(w, req)
		var response signStaticFilesResponse
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil {
			t.Fatalf("sign response: %d %s", w.Code, w.Body.String())
		}
		return response.URLs[path]
	}
	for _, prefix := range []string{"%61rtifacts/", "%73ubagent/artifact-blobs/"} {
		path := "/static-files/" + prefix + suffix
		if sign("victim", path) == "" {
			t.Fatalf("ordinary encoding rejected: %s", path)
		}
	}
	for _, purged := range []bool{false, true} {
		user := "attacker"
		if purged {
			if err := PurgeConversationOwned(svc.DB, "victim", []string{"private"}); err != nil {
				t.Fatal(err)
			}
			user = "victim"
		}
		for _, prefix := range []string{"artifacts/", "%61rtifacts/", "%2561rtifacts/", "%252561rtifacts/", "subagent/artifact-blobs/", "%73ubagent/artifact-blobs/", "%2573ubagent/artifact-blobs/", "subagent/%2561rtifact-blobs/"} {
			for _, origin := range []string{"", "https://example.invalid"} {
				path := origin + "/static-files/" + prefix + suffix
				if got := sign(user, path); got != "" {
					t.Errorf("purged=%v unauthorized path %q signed as %q", purged, path, got)
				}
			}
		}
	}
}

func TestArtifactStorageRootIsIndependentAndLegacyURLsRemainReadable(t *testing.T) {
	svc := New(v2TestDB(t).DB)
	ctx := context.Background()
	old, err := svc.CommitRevision(ctx, CommitRequest{OwnerUserID: "u1", Title: "old.txt", ContentType: "file", Content: []byte("old")})
	if err != nil {
		t.Fatal(err)
	}
	oldURL, _, err := SignRevisionURL(ctx, svc, "u1", old.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	// References created before the new namespace still resolve via legacy root.
	oldURL = strings.Replace(oldURL, "/static-files/artifacts/", "/static-files/subagent/artifact-blobs/", 1)
	root := t.TempDir()
	t.Setenv("LAZYMIND_ARTIFACT_STORAGE_ROOT", root)
	current, err := svc.CommitRevision(ctx, CommitRequest{OwnerUserID: "u1", Title: "new.txt", ContentType: "file", Content: []byte("new")})
	if err != nil {
		t.Fatal(err)
	}
	rev, _, err := svc.GetRevision(ctx, "u1", current.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var blob orm.ArtifactBlob
	if err := svc.DB.First(&blob, "id = ?", rev.BlobID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(blob.StorageKey, root+string(os.PathSeparator)) {
		t.Fatalf("blob used workspace: %s", blob.StorageKey)
	}
	read := func(rawURL, want string) {
		req := httptest.NewRequest(http.MethodGet, rawURL, nil)
		req = mux.SetURLVars(req, map[string]string{"path": strings.TrimPrefix(req.URL.Path, "/static-files/")})
		// Re-sign after changing a namespace, since the URL signature covers it.
		signed := doc.StaticFileURLFromAnyStoragePath(req.URL.Path)
		req.URL.RawQuery = strings.SplitN(signed, "?", 2)[1]
		w := httptest.NewRecorder()
		doc.GetSignedStaticFile(w, req)
		if w.Code != http.StatusOK || w.Body.String() != want {
			t.Fatalf("read %s: status=%d body=%s", req.URL.Path, w.Code, w.Body.String())
		}
	}
	read(oldURL, "old")
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	url, _, err := SignRevisionURL(ctx, svc, "u1", current.RevisionID)
	if err != nil || !strings.HasPrefix(url, "/static-files/artifacts/") {
		t.Fatalf("independent sign: %q %v", url, err)
	}
	read(url, "new")
	for _, rel := range []string{"artifacts/u1/../u2/blob", "artifacts/u1/../../outside", "artifacts/u1\\..\\u2/blob"} {
		if staticstorage.Authorized(ctx, rel, "u1") {
			t.Fatalf("authorized traversal %q", rel)
		}
	}
}

type signStaticFilesRequest struct {
	Paths []string `json:"paths"`
}
type signStaticFilesResponse struct {
	URLs map[string]string `json:"urls"`
}

func TestSignStaticFilesOmitsForeignArtifactBlobs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
	t.Setenv("LAZYMIND_FILE_URL_SIGN_SECRET", "doc-test-secret")
	owned := filepath.Join(root, "artifact-blobs", "user-1", "aa", "aabb")
	foreign := filepath.Join(root, "artifact-blobs", "user-2", "bb", "bbcc")
	for _, path := range []string{owned, foreign} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("blob"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	body, _ := json.Marshal(signStaticFilesRequest{Paths: []string{
		owned,
		foreign,
		"/static-files/subagent/artifact-blobs/user-2/bb/bbcc",
	}})
	req := httptest.NewRequest(http.MethodPost, "/static-files:sign", strings.NewReader(string(body)))
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	doc.SignStaticFiles(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp signStaticFilesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.URLs[owned] == "" {
		t.Fatalf("owned blob missing signed URL: %#v", resp.URLs)
	}
	if _, signed := resp.URLs[foreign]; signed {
		t.Fatalf("foreign blob path was signed: %#v", resp.URLs)
	}
	if _, signed := resp.URLs["/static-files/subagent/artifact-blobs/user-2/bb/bbcc"]; signed {
		t.Fatalf("foreign blob URL was signed: %#v", resp.URLs)
	}
}

func TestSignStaticFilesOmitsPrefixedForeignArtifactBlobURLs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
	t.Setenv("LAZYMIND_FILE_URL_SIGN_SECRET", "doc-test-secret")
	foreign := filepath.Join(root, "artifact-blobs", "user-2", "bb", "bbcc")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("blob"), 0o644); err != nil {
		t.Fatal(err)
	}
	prefixed := "https://anything.invalid/static-files/subagent/artifact-blobs/user-2/bb/bbcc"
	body, _ := json.Marshal(signStaticFilesRequest{Paths: []string{prefixed}})
	req := httptest.NewRequest(http.MethodPost, "/static-files:sign", strings.NewReader(string(body)))
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	doc.SignStaticFiles(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp signStaticFilesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if _, signed := resp.URLs[prefixed]; signed {
		t.Fatalf("prefixed foreign blob URL was signed: %#v", resp.URLs)
	}
}
