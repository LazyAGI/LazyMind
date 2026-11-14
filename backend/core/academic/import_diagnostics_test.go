package academic

import (
	"errors"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestImportItemFailureDistinguishesSavedMetadataFromPDF(t *testing.T) {
	for _, tc := range []struct {
		name string
		item orm.PaperImportItem
		want string
	}{
		{"unsaved", orm.PaperImportItem{Stage: "pdf_lookup"}, "论文尚未入库"},
		{"metadata", orm.PaperImportItem{Stage: "pdf_lookup", LibraryPaperID: "paper", ResultKind: "enrichment"}, "题录已保存"},
		{"pdf", orm.PaperImportItem{Stage: "parse", LibraryPaperID: "paper", DocumentID: "pdf", ResultKind: "enrichment"}, "PDF 已保存"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := importItemFailure(errors.New("PDF unavailable"), tc.item)
			if !strings.Contains(got.Message, tc.want) {
				t.Fatalf("wrong saved state: %+v", got)
			}
			if tc.name == "metadata" && (strings.Contains(got.Message, "尚未入库") || !strings.Contains(got.Recovery, "无需重新导入题录")) {
				t.Fatalf("misleading enrichment failure: %+v", got)
			}
		})
	}
}
