package doc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

// A complete one-page PDF, including valid object offsets and trailer.
func revisionTestPDF() []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, object := range []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << >> >>",
	} {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := b.Len()
	b.WriteString("xref\n0 4\n0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 4 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return b.Bytes()
}

func TestRevisePDFTranslationPublishesDespiteDiagnosticMismatch(t *testing.T) {
	for _, test := range []struct {
		name, body  string
		renderError bool
	}{
		{"rerender", `{"rerender":true}`, false},
		{"override", `{"overrides":{"block":"Revised"}}`, false},
		{"render error", `{"rerender":true}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := newDocumentTestDB(t)
			seedDocumentListDataset(t, db, "ds", "owner")
			dir := t.TempDir()
			base := pdfArtifactRecord{ID: "base", Kind: pdfArtifactTranslation, Filename: "translated.pdf", TargetLanguage: "en", SourcePath: filepath.Join(dir, "source.pdf"), StoredPath: filepath.Join(dir, "old.pdf"), LayoutPath: filepath.Join(dir, "layout.json"), DraftPath: filepath.Join(dir, "draft.json")}
			for path, data := range map[string][]byte{base.SourcePath: revisionTestPDF(), base.StoredPath: revisionTestPDF(), base.LayoutPath: []byte(`{"blocks":[]}`)} {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			draft := pdfTranslationDraft{ArtifactID: base.ID, TargetLanguage: "en", Blocks: []pdfTranslationDraftBlock{{ID: "block", SourceText: "Original", TranslatedText: "Translated"}}}
			if err := writePDFTranslationDraft(base.DraftPath, draft); err != nil {
				t.Fatal(err)
			}
			seedDocumentListDoc(t, db, "ds", "doc", "paper.pdf", time.Now(), "owner", mustJSON(documentExt{StoredPath: base.SourcePath, PDFArtifacts: []pdfArtifactRecord{base}}))
			output := revisionTestPDF()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]string
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if err := os.WriteFile(request["output_path"], output, 0600); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				if err := os.WriteFile(request["output_path"]+".diagnostics.json", []byte("diagnostics"), 0600); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				if test.renderError {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"warning_count":2,"rendered_block_count":1,"requested_block_count":2}`)
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_OFFICE_CONVERT_URL", server.URL+"/v1/office/to-pdf")
			request := httptest.NewRequest(http.MethodPost, "/revision", strings.NewReader(test.body))
			request.Header.Set("X-User-Id", "owner")
			request = mux.SetURLVars(request, map[string]string{"dataset": "ds", "document": "doc", "artifact": "base"})
			response := httptest.NewRecorder()
			RevisePDFTranslation(response, request)
			if test.renderError {
				if response.Code != http.StatusBadGateway {
					t.Fatalf("unexpected error response: %d", response.Code)
				}
				leftovers, err := filepath.Glob(filepath.Join(dir, "*.diagnostics.json"))
				if err != nil || len(leftovers) != 0 {
					t.Fatalf("failed render diagnostics leaked: %v %v", leftovers, err)
				}
				return
			}
			if response.Code != http.StatusOK {
				t.Fatalf("generated PDF rejected: %d %s", response.Code, response.Body.String())
			}
			var result struct {
				Data pdfArtifactRecord `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Data.WarningCount != 3 {
				t.Fatalf("warning count = %d, want 3", result.Data.WarningCount)
			}
			saved, err := os.ReadFile(result.Data.StoredPath)
			if err != nil || !bytes.Equal(saved, output) {
				t.Fatalf("generated PDF not retained: %v", err)
			}
			var row orm.Document
			if err := db.Where("id=?", "doc").Take(&row).Error; err != nil {
				t.Fatal(err)
			}
			var ext documentExt
			if err := json.Unmarshal(row.Ext, &ext); err != nil {
				t.Fatal(err)
			}
			if len(ext.PDFArtifacts) != 1 || ext.PDFArtifacts[0].ID != result.Data.ID || ext.PDFArtifacts[0].WarningCount != 3 {
				t.Fatalf("revision not persisted: %+v", ext.PDFArtifacts)
			}
		})
	}
}
