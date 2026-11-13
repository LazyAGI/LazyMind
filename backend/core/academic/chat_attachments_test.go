package academic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

func TestPaperChatLaunchUsesAllPDFsAndPromptWithoutSpecialBindings(t *testing.T) {
	db := paperFixture(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	input := ReviewWorkflowInput{UserInstruction: "比较两篇方法，8页", Papers: []WorkflowPaper{}}
	for _, id := range []string{"a", "b"} {
		path := filepath.Join(doc.UploadRoot(), id+".pdf")
		if err := os.WriteFile(path, []byte("%PDF-1.4\noriginal "+id), 0600); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&orm.Document{}).Where("id=?", id).Update("ext", marshal(map[string]string{"stored_path": path})).Error; err != nil {
			t.Fatal(err)
		}
		input.Papers = append(input.Papers, WorkflowPaper{Paper: PaperRef{PaperID: id, Title: "Same title", SourceDocumentID: id}, IsSeed: true})
	}
	handoff, err := createPaperHandoff(t.Context(), db.DB, "u", "papers", "", 0, "ppt", "attachments", input, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var task PaperTaskInput
	if err := json.Unmarshal(handoff.InputJSON, &task); err != nil {
		t.Fatal(err)
	}
	task.Notes = []PaperTaskNote{{PaperID: "a", Title: "Reading note", Content: "保留我的方法疑问"}}
	if err := db.Model(&handoff).Update("input_json", marshal(task)).Error; err != nil {
		t.Fatal(err)
	}
	files, prompt, err := paperChatAttachments(t.Context(), db.DB, "u", handoff.ConversationID)
	if err != nil || len(files) != 2 {
		t.Fatalf("attachments: %v, %v", files, err)
	}
	for i, id := range []string{"a", "b"} {
		path := files[i]["uri"].(string)
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "%PDF-1.4\noriginal "+id {
			t.Fatalf("lost original %s: %s %v", id, data, err)
		}
		if files[i]["input_type"] != "file" || !strings.Contains(prompt, files[i]["filename"].(string)) {
			t.Fatal(files, prompt)
		}
		if _, err := doc.ValidateChatSourceAttachments(t.Context(), db.DB, doc.DatasetCatalogCaller{UserID: "u"}, []doc.ChatSourceAttachmentReference{{Path: path}}); err != nil {
			t.Fatal("not a normal owned attachment", err)
		}
		if _, err := doc.ValidateChatSourceAttachments(t.Context(), db.DB, doc.DatasetCatalogCaller{UserID: "other"}, []doc.ChatSourceAttachmentReference{{Path: path}}); err == nil {
			t.Fatal("foreign user can read attachment")
		}
	}
	if !strings.Contains(prompt, "先基于全部论文") || !strings.Contains(prompt, task.Notes[0].Content) {
		t.Fatal("lost instructions or notes", prompt)
	}
	if strings.Contains(prompt, "task_context") || strings.Contains(prompt, "handoff_id") {
		t.Fatal("special manager context leaked", prompt)
	}
	retry, _, err := paperChatAttachments(t.Context(), db.DB, "u", handoff.ConversationID)
	if err != nil || retry[0]["uri"] != files[0]["uri"] || retry[1]["uri"] != files[1]["uri"] {
		t.Fatal("retry changed attachments", retry, err)
	}
	var count int64
	db.Model(&orm.UploadedFile{}).Count(&count)
	if count != 2 {
		t.Fatal("retry duplicated uploads", count)
	}
	if _, _, err := paperChatAttachments(t.Context(), db.DB, "other", handoff.ConversationID); err == nil {
		t.Fatal("foreign task was accessible")
	}
	if err := db.Model(&orm.Dataset{}).Where("id=?", "papers").Update("create_user_id", "other").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := paperChatAttachments(t.Context(), db.DB, "u", handoff.ConversationID); err == nil {
		t.Fatal("revoked dataset access was ignored")
	}
}

func TestPaperChatSnapshotPreservesAllPagesAndRejectsIncompleteSources(t *testing.T) {
	db := paperFixture(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	input := ReviewWorkflowInput{Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "a", Title: "Research", SourceDocumentID: "a"}, IsSeed: true}}}
	sources, err := seedSources(db.DB, "papers", input.Papers)
	if err != nil {
		t.Fatal(err)
	}
	pages := marshal([]ContentPage{{Page: 1, Text: "First page evidence"}, {Page: 9, Text: "Last page limitations"}})
	digest := sha256.Sum256(pages)
	content := orm.AcademicPaperContent{ID: "frozen", UserID: "u", PaperID: "a", SourceKey: sources[0].key(), SourceKind: "kb", SourceDatasetID: "papers", SourceDocumentID: "a", Status: "ready", ContentHash: hex.EncodeToString(digest[:]), PagesJSON: pages,
		QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"})}
	if err := db.Create(&content).Error; err != nil {
		t.Fatal(err)
	}
	handoff, err := createPaperHandoff(t.Context(), db.DB, "u", "papers", "", 0, "ppt", "snapshot", input, []orm.AcademicPaperContent{content}, false)
	if err != nil {
		t.Fatal(err)
	}
	files, prompt, err := paperChatAttachments(t.Context(), db.DB, "u", handoff.ConversationID)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	data, err := os.ReadFile(files[0]["uri"].(string))
	if err != nil || !strings.Contains(string(data), "Page 9\n\nLast page limitations") || !strings.Contains(string(data), "First page evidence") {
		t.Fatal("lost fulltext pages", string(data), err)
	}
	if !strings.Contains(prompt, "完整正文快照，无原 PDF 图像") || strings.Contains(prompt, "先基于全部论文") {
		t.Fatal("wrong task instructions", prompt)
	}
	for _, status := range []string{"processing", "unavailable", "failed", "needs_review"} {
		content.Status = status
		if err := db.Save(&content).Error; err != nil {
			t.Fatal(err)
		}
		if _, _, err := paperAttachmentBytes(t.Context(), db.DB, "u", sources[0], []orm.AcademicPaperContent{content}); err == nil {
			t.Fatal("accepted incomplete fulltext", status)
		}
	}
	content.Status = "ready"
	content.PagesJSON = marshal([]ContentPage{{Page: 1, Text: ""}})
	if err := db.Save(&content).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := paperAttachmentBytes(t.Context(), db.DB, "u", sources[0], []orm.AcademicPaperContent{content}); err == nil {
		t.Fatal("accepted empty fulltext")
	}
}

func TestPaperChatRejectsChangedOrEscapedOriginal(t *testing.T) {
	db := paperFixture(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	source := paperSource{Paper: PaperRef{PaperID: "a"}, DatasetID: "papers", DocumentID: "a", Revision: 1, IsSeed: true}
	for _, failure := range []string{"hash", "escape", "missing", "identity"} {
		t.Run(failure, func(t *testing.T) {
			directory := doc.UploadRoot()
			if failure == "escape" {
				directory = t.TempDir()
			}
			path := filepath.Join(directory, "source.pdf")
			if failure != "missing" {
				if err := os.WriteFile(path, []byte("%PDF-1.4\nsource"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				path = filepath.Join(directory, "missing.pdf")
			}
			if err := db.Model(&orm.Document{}).Where("id=?", "a").Update("ext", marshal(map[string]string{"stored_path": path})).Error; err != nil {
				t.Fatal(err)
			}
			selected := source
			if failure == "hash" {
				selected.Hash = "different"
			}
			if failure == "identity" {
				selected.Revision = 2
			}
			if _, _, err := paperAttachmentBytes(t.Context(), db.DB, "u", selected, nil); err == nil {
				t.Fatal("accepted invalid original", failure)
			}
		})
	}
}
