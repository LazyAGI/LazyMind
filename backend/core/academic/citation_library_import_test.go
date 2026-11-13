package academic

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
)

func TestPaperLinkPreservesCitationReferenceFromPreview(t *testing.T) {
	db := paperFixture(t)
	token, err := saveImportPreview(t.Context(), "u", "papers", []PreviewItem{{WorkID: "a", Paper: testVerifiedPaper(), ReferenceIDs: []string{"reference-a"}, Disposition: "metadata_only"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]any{
		"entry_type": "paper_link", "target_dataset_id": "papers", "preview_token": token,
		"idempotency_key": "citation-without-pdf",
		"items":           []map[string]any{{"work_id": "a", "reference_ids": []string{"reference-a"}}},
	}
	response := httptest.NewRecorder()
	CreateImport(response, paperRequest(string(marshal(request))))
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var item orm.PaperImportItem
	if err := db.Where("academic_work_id=?", "a").Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	var references []string
	if err := json.Unmarshal(item.ReferenceIDsJSON, &references); err != nil || len(references) != 1 || references[0] != "reference-a" {
		t.Fatalf("source citation was not preserved: %+v %v", references, err)
	}
}

func TestPaperLinkRejectsReferenceOutsidePreview(t *testing.T) {
	db := paperFixture(t)
	token, err := saveImportPreview(t.Context(), "u", "papers", []PreviewItem{{WorkID: "a", Paper: testVerifiedPaper(), ReferenceIDs: []string{"reference-a"}, Disposition: "metadata_only"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]any{
		"entry_type": "paper_link", "target_dataset_id": "papers", "preview_token": token,
		"idempotency_key": "unverified-reference",
		"items":           []map[string]any{{"work_id": "a", "reference_ids": []string{"reference-b"}}},
	}
	response := httptest.NewRecorder()
	CreateImport(response, paperRequest(string(marshal(request))))
	if response.Code != 422 {
		t.Fatalf("reference missing from preview was accepted: status=%d body=%s", response.Code, response.Body.String())
	}
	var count int64
	db.Model(&orm.PaperLibraryEntry{}).Where("dataset_id=?", "papers").Count(&count)
	if count != 0 {
		t.Fatal("unverified reference created library membership")
	}
}
