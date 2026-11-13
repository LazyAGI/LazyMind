package academic

import (
	"encoding/json"
	"strings"
	"testing"

	"lazymind/core/paperlibrary"
)

func TestRestoredPaperWritableMetadataPreservesAuxiliaryGraph(t *testing.T) {
	db := paperLibraryTestDB(t)
	raw := json.RawMessage(`{"schemaVersion":4,"papers":[{"id":"restored","metadata":{"title":"Restored study","authors":["Researcher"],"year":2025,"identifiers":{"doi":"10.1234/restored","pmid":"77"},"confidence":{"title":0.9}},"source":"local"}],"folders":[],"notes":[{"id":"note","paperId":"restored","title":"Notes","content":"Keep my notes","kind":"note"}],"attachments":[{"id":"missing","paperId":"restored","name":"old.pdf","kind":"pdf","available":false}]}`)
	if err := paperlibrary.ImportSnapshot(t.Context(), db, "papers", "owner", raw); err != nil {
		t.Fatal(err)
	}
	paper := readLibrary(t, "papers").Data.Papers[0]
	viewResponse := libraryRequest(t, "GET", "/library", "papers", "owner", nil)
	var view struct {
		Data struct {
			Papers []map[string]any `json:"papers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(viewResponse.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	legacyMetadata := view.Data.Papers[0]["metadata"]
	rejected := libraryRequest(t, "PATCH", "/library/papers/restored", "papers", "owner", map[string]any{"revision": paper.Revision, "metadata": legacyMetadata})
	if rejected.Code != 400 {
		t.Fatalf("strict API must reject auxiliary fields: %d %s", rejected.Code, rejected.Body.String())
	}
	// Mutation DTOs accept the writable projection, while serialized backup views
	// also carry auxiliary metadata that must not be echoed into strict requests.
	paper.Metadata.Title = "Corrected study"
	w := libraryRequest(t, "PATCH", "/library/papers/restored", "papers", "owner", map[string]any{"revision": paper.Revision, "metadata": paper.Metadata})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	paper = readLibrary(t, "papers").Data.Papers[0]
	uploadReceipt(t, db, "papers", "replacement", "replacement-upload", paper.ID, "replacement-hash")
	confirmUpload(t, paper, "replacement", "replacement-upload", true)
	response := libraryRequest(t, "GET", "/library", "papers", "owner", nil)
	for _, preserved := range []string{`"pmid":"77"`, `"confidence":{"title":0.9}`, `Keep my notes`, `Corrected study`, `replacement`} {
		if !strings.Contains(response.Body.String(), preserved) {
			t.Fatalf("missing %s in %s", preserved, response.Body.String())
		}
	}
	if got := readLibrary(t, "papers"); got.PaperCount != 1 || got.PDFAttachmentCount != 1 {
		t.Fatalf("unexpected library counts: %+v", got.Counts)
	}
}
