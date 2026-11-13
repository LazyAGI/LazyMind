package workflowtool

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/executor"
)

func TestPinnedPDFResourceHydration(t *testing.T) {
	db := orm.OpenTestDB(t).DB
	if err := db.AutoMigrate(&orm.WorkflowInputResource{}); err != nil {
		t.Fatal(err)
	}
	content := []byte("%PDF-1.7\nsource")
	hash := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	resource := orm.WorkflowInputResource{ID: "pdf", OwnerUserID: "owner", Name: "paper.pdf", Revision: 1,
		Content: content, Size: int64(len(content)), ContentHash: hash, MimeType: "application/pdf"}
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	bound := map[string]any{"source_type": "input_resource", "source_id": "pdf", "source_revision": "1", "content_hash": hash}
	contract := executor.AttemptContext{Inputs: map[string]any{"uploaded_materials": []any{bound}}}
	service := &Service{DB: db}
	args := map[string]any{"resource_ref": "resource:pdf", "start_page": 2}
	got, err := service.resolveFileArguments(t.Context(), "owner", contract, "pdf_read_text", args)
	if err != nil || got["content_base64"] != base64.StdEncoding.EncodeToString(content) || got["filename"] != "paper.pdf" || got["resource_ref"] != nil || got["start_page"] != 2 {
		t.Fatalf("reference did not resolve to exact pinned bytes: %v %v", got, err)
	}
	if args["resource_ref"] != "resource:pdf" || args["content_base64"] != nil {
		t.Fatal("hydration mutated receipt arguments")
	}
	for _, test := range []struct {
		name, owner string
		contract    executor.AttemptContext
		arguments   map[string]any
	}{
		{"foreign owner", "other", contract, args},
		{"unbound file", "owner", executor.AttemptContext{}, args},
		{"ambiguous bytes", "owner", contract, map[string]any{"resource_ref": "resource:pdf", "content_base64": "ignored"}},
		{"server path", "owner", contract, map[string]any{"resource_ref": "/etc/passwd"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.resolveFileArguments(t.Context(), test.owner, test.contract, "pdf_read_text", test.arguments); err == nil {
				t.Fatal("unsafe reference was accepted")
			}
		})
	}
	bound["source_revision"] = "2"
	if _, err := service.resolveFileArguments(t.Context(), "owner", contract, "pdf_extract_images", args); err == nil {
		t.Fatal("changed revision was accepted")
	}
	bound["source_revision"] = "1"
	if err := db.Model(&resource).Update("content", []byte("corrupted bytes")).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.resolveFileArguments(t.Context(), "owner", contract, "pdf_read_text", args); err == nil {
		t.Fatal("corrupt content was accepted")
	}
}
