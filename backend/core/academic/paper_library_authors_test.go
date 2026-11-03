package academic

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"lazymind/core/paperlibrary"
)

func TestLibraryMetadataEditPreservesLargeCollaborationAuthors(t *testing.T) {
	db := paperLibraryTestDB(t)
	authors := make([]string, 1350)
	for i := range authors {
		authors[i] = fmt.Sprintf("Author %04d", i)
	}
	authors[21] = authors[20]
	raw := marshal(map[string]any{"papers": []any{map[string]any{
		"id": "collaboration", "metadata": paperlibrary.Metadata{Title: "Collaboration paper", Authors: authors}, "source": "zotero",
	}}})
	if err := paperlibrary.ImportSnapshot(t.Context(), db, "papers", "owner", raw); err != nil {
		t.Fatal(err)
	}
	paper := readLibrary(t, "papers").Data.Papers[0]
	paper.Metadata.Title = "Corrected collaboration title"
	w := libraryRequest(t, "PATCH", "/library/papers/collaboration", "papers", "owner", map[string]any{"revision": paper.Revision, "metadata": paper.Metadata})
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	saved := readLibrary(t, "papers").Data.Papers[0]
	if saved.Metadata.Title != "Corrected collaboration title" || saved.Revision != paper.Revision+1 || !reflect.DeepEqual(saved.Metadata.Authors, authors) {
		t.Fatal("metadata edit lost author count, order, or title correction")
	}
}

func TestLibraryMetadataAuthorLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		count   int
		length  int
		wantErr bool
	}{
		{name: "large collaboration", count: 10000, length: 20},
		{name: "too many authors", count: 10001, length: 20, wantErr: true},
		{name: "maximum name bytes", count: 1, length: 2048},
		{name: "oversized name", count: 1, length: 2049, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authors := make([]string, tc.count)
			for i := range authors {
				authors[i] = strings.Repeat("a", tc.length)
			}
			metadata := paperlibrary.Metadata{Title: "Paper", Authors: authors}
			if err := validateLibraryMetadata(&metadata); (err != nil) != tc.wantErr {
				t.Fatalf("validation error = %v, want error = %v", err, tc.wantErr)
			}
		})
	}
}
