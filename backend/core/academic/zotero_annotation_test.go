package academic

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"lazymind/core/paperlibrary"
)

func TestZoteroAPIPreviewPreservesAnnotationQuoteAndPosition(t *testing.T) {
	const position = `{"pageIndex":7,"rects":[[31.5,81,210,94],[31.5,65,190,78]]}`
	annotation := map[string]any{"key": "ANNOT001", "data": map[string]any{"key": "ANNOT001", "itemType": "annotation", "parentItem": "ATTACH01", "annotationText": "原文高亮内容", "annotationComment": "My comment", "annotationPageLabel": "304", "annotationColor": "#ffd400", "annotationPosition": position}}
	client := zoteroTestClient(t, zoteroConfig{Source: "remote", LibraryType: "users", LibraryID: "123", APIKey: "test-key"}, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/collections") {
			return zoteroResponse(r, 200, `[]`), nil
		}
		return zoteroResponse(r, 200, string(marshal([]any{annotation}))), nil
	})
	preview, err := client.preview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	items := previewDataForTest(t, preview.Items)
	got := items["ANNOT001"]
	if got["annotationPosition"] != position || got["annotationText"] != "原文高亮内容" || got["annotationPageLabel"] != "304" {
		t.Fatalf("API preview lost annotation: %v", got)
	}
}

func TestZoteroAnnotationGeometrySurvivesSnapshotAndLegacyEnrichment(t *testing.T) {
	paperLibraryTestDB(t)
	data := paperlibrary.EmptyData()
	data.Papers = []paperlibrary.Paper{*libraryDraft("paper", "Paper", "")}
	data.Attachments = []paperlibrary.Attachment{{ID: "attachment", PaperID: "paper", Name: "paper.pdf", Kind: "pdf", Source: "zotero"}}
	geometry := [][4]float64{{31.5, 81, 210, 94}, {31.5, 65, 190, 78}}
	annotation := map[string]any{"id": "annotation", "paperId": "paper", "attachmentId": "attachment", "quote": "原文高亮内容", "comment": "My comment", "page": 8, "color": "yellow"}
	save := func(key string) *httptest.ResponseRecorder {
		t.Helper()
		data.Extra = marshal(map[string]any{"annotations": []any{annotation}})
		req := httptest.NewRequest(http.MethodPost, "/academic/zotero/import?target_dataset_id=papers", strings.NewReader(string(marshal(map[string]any{"source_id": "users:123", "idempotency_key": key, "data": data}))))
		req.Header.Set("X-User-Id", "owner")
		w := httptest.NewRecorder()
		ImportZoteroSnapshot(w, req)
		if w.Code != 200 {
			t.Fatalf("snapshot import: %d %s", w.Code, w.Body.String())
		}
		return w
	}
	save("old-client") // Existing imports already contain quote/page but no geometry.
	annotation["pdfRects"] = geometry
	for _, key := range []string{"geometry-repair", "geometry-repair"} {
		w := save(key)
		assertZoteroAnnotationGeometry(t, w.Body.Bytes(), geometry)
	}
	w := libraryRequest(t, "GET", "/library?include_processing=false", "papers", "owner", nil)
	if w.Code != 200 {
		t.Fatalf("reload: %d %s", w.Code, w.Body.String())
	}
	assertZoteroAnnotationGeometry(t, w.Body.Bytes(), geometry)
}

func assertZoteroAnnotationGeometry(t *testing.T, raw []byte, want [][4]float64) {
	t.Helper()
	var response struct {
		Data struct {
			Annotations []struct {
				ID, PaperID, AttachmentID, Quote, Comment, Color string
				Page                                             int
				PDFRects                                         [][4]float64 `json:"pdfRects"`
			} `json:"annotations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Annotations) != 1 {
		t.Fatalf("annotations duplicated/lost: %s", raw)
	}
	got := response.Data.Annotations[0]
	if got.ID != "annotation" || got.PaperID != "paper" || got.AttachmentID != "attachment" || got.Quote != "原文高亮内容" || got.Comment != "My comment" || got.Color != "yellow" || got.Page != 8 || !reflect.DeepEqual(got.PDFRects, want) {
		t.Fatalf("annotation body/location lost: %+v", got)
	}
}
