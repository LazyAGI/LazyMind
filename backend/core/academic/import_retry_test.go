package academic

import (
	"encoding/json"
	"io"
	"lazymind/core/common/orm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLinkPreviewKeepsRequestedVersionWhenIdentityAlreadyExists(t *testing.T) {
	db := paperFixture(t)
	if err := db.Model(&orm.AcademicWork{}).Where("id=?", "a").Updates(map[string]any{"arxiv_id_base": "1706.03762", "metadata_provenance_json": marshal(map[string]string{"pdf_version": "v7"})}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.AcademicWorkDocument{AcademicWorkID: "a", DatasetID: "papers", DocumentID: "a", SourceProvider: "arxiv", SourceVersion: "v7"}).Error; err != nil {
		t.Fatal(err)
	}
	queries := []string{}
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "export.arxiv.org" {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header), Request: r}, nil
		}
		queries = append(queries, r.URL.Query().Get("id_list"))
		body := `<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>https://arxiv.org/abs/1706.03762v6</id><title>Version six title</title><summary>Version six abstract</summary><published>2017-01-01T00:00:00Z</published><author><name>A. Author</name></author></entry></feed>`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	w := httptest.NewRecorder()
	PreviewImport(w, paperRequest(`{"identifier":"https://arxiv.org/abs/1706.03762v6","target_dataset_id":"papers"}`))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Items []PreviewItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Items) != 1 {
		t.Fatal("invalid preview", err, w.Body.String())
	}
	item := result.Items[0]
	if item.Disposition != "downloadable" || len(item.Candidates) != 1 || item.Candidates[0].Version != "v6" || item.Paper.SourceVersion != "v6" || item.Paper.Title != "Version six title" {
		t.Fatalf("existing shared identity replaced the requested attachment: %+v", item)
	}
	if len(queries) != 1 || queries[0] != "1706.03762v6" {
		t.Fatalf("resolved the wrong version or queried link metadata twice: %v", queries)
	}
}
