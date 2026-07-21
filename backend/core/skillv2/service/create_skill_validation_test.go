package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestCreateSkillFromUploadedZip_RequiresSkillMD(t *testing.T) {
	db := newSkillV2TestDB(t)
	zipPath := filepath.Join(t.TempDir(), "missing-skill-md.zip")
	writeSkillZip(t, zipPath, map[string][]byte{
		"references/a.md": []byte("# 参考资料\n"),
	})
	uploadStore := newFakeUploadStore()
	uploadStore.Put(UploadSession{
		UploadID:    "upload_missing_skill_md",
		OwnerUserID: "user_001",
		State:       "completed",
		StoredPath:  zipPath,
		Filename:    "skill.zip",
	})
	svc := newCreateSkillValidationService(t, db, uploadStore)

	_, err := svc.CreateSkill(context.Background(), validCreateSkillRequest("upload_missing_skill_md"))
	if err == nil {
		t.Fatal("CreateSkill succeeded for package without SKILL.md")
	}
	assertNoSkillTruthRows(t, db)
}

func TestCreateSkillFromUploadedZip_RequiresFrontmatterMetadata(t *testing.T) {
	for name, content := range map[string][]byte{
		"frontmatter": []byte("# Skill\n"),
		"name":        []byte("---\ndescription: description\n---\n# Skill\n"),
		"description": []byte("---\nname: skill\n---\n# Skill\n"),
	} {
		t.Run(name, func(t *testing.T) {
			db := newSkillV2TestDB(t)
			zipPath := filepath.Join(t.TempDir(), name+".zip")
			writeSkillZip(t, zipPath, map[string][]byte{"SKILL.md": content})
			uploadStore := newFakeUploadStore()
			uploadID := "upload_missing_" + name
			uploadStore.Put(UploadSession{UploadID: uploadID, OwnerUserID: "user_001", State: "completed", StoredPath: zipPath, Filename: name + ".zip"})
			svc := newCreateSkillValidationService(t, db, uploadStore)

			if _, err := svc.CreateSkill(context.Background(), validCreateSkillRequest(uploadID)); err == nil {
				t.Fatal("CreateSkill succeeded")
			}
			assertNoSkillTruthRows(t, db)
		})
	}
}

func TestCreateSkillFromUploadedZip_AllowsSingleTopLevelDirectory(t *testing.T) {
	db := newSkillV2TestDB(t)
	zipPath := filepath.Join(t.TempDir(), "wrapped.zip")
	writeSkillZip(t, zipPath, map[string][]byte{
		"openclaw-openclaw-changelog-update/SKILL.md":        externalSkillMD("openclaw-openclaw-changelog-update", "OpenClaw changelog update"),
		"openclaw-openclaw-changelog-update/references/a.md": []byte("# A\n"),
	})
	uploadStore := newFakeUploadStore()
	uploadStore.Put(UploadSession{
		UploadID:    "upload_wrapped_skill",
		OwnerUserID: "user_001",
		State:       "completed",
		StoredPath:  zipPath,
		Filename:    "wrapped.zip",
	})
	svc := newCreateSkillValidationService(t, db, uploadStore)

	resp, err := svc.CreateSkill(context.Background(), validCreateSkillRequest("upload_wrapped_skill"))
	if err != nil {
		t.Fatalf("CreateSkill returned error: %v", err)
	}
	entries := listRevisionEntries(t, db, resp.HeadRevisionID)
	if _, ok := entries["SKILL.md"]; !ok {
		t.Fatal("revision entries missing normalized SKILL.md")
	}
	if _, ok := entries["references/a.md"]; !ok {
		t.Fatal("revision entries missing normalized references/a.md")
	}
	if _, ok := entries["openclaw-openclaw-changelog-update/SKILL.md"]; ok {
		t.Fatal("revision entries kept wrapper directory path")
	}
	skillBlob := getBlobByPath(t, db, resp.HeadRevisionID, "SKILL.md")
	if skillBlob.StorageBackend != "postgres" || len(skillBlob.Content) == 0 || skillBlob.StorageKey != nil {
		t.Fatalf("SKILL.md blob storage invalid: %#v", skillBlob)
	}
}

func TestCreateSkillFromUploadedZip_RejectsUnsafePathCases(t *testing.T) {
	cases := map[string]string{
		"dotdot":        "../evil.md",
		"absolute":      "/abs/path.md",
		"emptySegment":  "references//a.md",
		"backslashPath": `references\a.md`,
	}

	for name, unsafePath := range cases {
		t.Run(name, func(t *testing.T) {
			db := newSkillV2TestDB(t)
			zipPath := filepath.Join(t.TempDir(), name+".zip")
			writeSkillZip(t, zipPath, map[string][]byte{
				"SKILL.md": []byte("# 论文精读\n"),
				unsafePath: []byte("bad path"),
			})
			uploadStore := newFakeUploadStore()
			uploadStore.Put(UploadSession{
				UploadID:    "upload_" + name,
				OwnerUserID: "user_001",
				State:       "completed",
				StoredPath:  zipPath,
				Filename:    "skill.zip",
			})
			svc := newCreateSkillValidationService(t, db, uploadStore)

			_, err := svc.CreateSkill(context.Background(), validCreateSkillRequest("upload_"+name))
			if err == nil {
				t.Fatalf("CreateSkill succeeded for unsafe path %q", unsafePath)
			}
			assertNoSkillTruthRows(t, db)
		})
	}
}

func TestCreateSkillFromUploadedZip_RejectsForeignUpload(t *testing.T) {
	db := newSkillV2TestDB(t)
	zipPath := filepath.Join(t.TempDir(), "skill.zip")
	writeSkillZip(t, zipPath, map[string][]byte{
		"SKILL.md": []byte("# 论文精读\n"),
	})
	uploadStore := newFakeUploadStore()
	uploadStore.Put(UploadSession{
		UploadID:    "upload_foreign",
		OwnerUserID: "user_002",
		State:       "completed",
		StoredPath:  zipPath,
		Filename:    "skill.zip",
	})
	svc := newCreateSkillValidationService(t, db, uploadStore)

	req := validCreateSkillRequest("upload_foreign")
	req.Source.StoredPath = filepath.Join(t.TempDir(), "attacker-controlled.zip")
	_, err := svc.CreateSkill(context.Background(), req)
	if err == nil {
		t.Fatal("CreateSkill succeeded for upload owned by another user")
	}
	assertNoSkillTruthRows(t, db)
}

func TestCreateSkillFromUploadedZip_RejectsUnfinishedUpload(t *testing.T) {
	for _, state := range []string{"pending", "failed"} {
		t.Run(state, func(t *testing.T) {
			db := newSkillV2TestDB(t)
			zipPath := filepath.Join(t.TempDir(), "skill.zip")
			writeSkillZip(t, zipPath, map[string][]byte{
				"SKILL.md": []byte("# 论文精读\n"),
			})
			uploadStore := newFakeUploadStore()
			uploadStore.Put(UploadSession{
				UploadID:    "upload_" + state,
				OwnerUserID: "user_001",
				State:       state,
				StoredPath:  zipPath,
				Filename:    "skill.zip",
			})
			svc := newCreateSkillValidationService(t, db, uploadStore)

			_, err := svc.CreateSkill(context.Background(), validCreateSkillRequest("upload_"+state))
			if err == nil {
				t.Fatalf("CreateSkill succeeded for upload state %q", state)
			}
			assertNoSkillTruthRows(t, db)
		})
	}
}

func TestCreateSkillFromUploadedZip_SupportsChineseFileNames(t *testing.T) {
	db := newSkillV2TestDB(t)
	zipPath := filepath.Join(t.TempDir(), "skill.zip")
	writeSkillZip(t, zipPath, map[string][]byte{
		"SKILL.md":   externalSkillMD("论文精读", "用于阅读和总结论文的技能"),
		"参考资料/示例.md": []byte("# 示例\n\n中文路径正文。\n"),
	})
	uploadStore := newFakeUploadStore()
	uploadStore.Put(UploadSession{
		UploadID:    "upload_chinese_names",
		OwnerUserID: "user_001",
		State:       "completed",
		StoredPath:  zipPath,
		Filename:    "skill.zip",
	})
	svc := newCreateSkillValidationService(t, db, uploadStore)

	resp, err := svc.CreateSkill(context.Background(), validCreateSkillRequest("upload_chinese_names"))
	if err != nil {
		t.Fatalf("CreateSkill returned error: %v", err)
	}
	entries := listRevisionEntries(t, db, resp.HeadRevisionID)
	if _, ok := entries["参考资料"]; !ok {
		t.Fatal("revision entries missing Chinese directory 参考资料")
	}
	if _, ok := entries["参考资料/示例.md"]; !ok {
		t.Fatal("revision entries missing Chinese file 参考资料/示例.md")
	}

	tree, err := svc.GetTree(context.Background(), TreeRef{SkillID: resp.SkillID, RefType: "head"})
	if err != nil {
		t.Fatalf("GetTree returned error: %v", err)
	}
	nodes := map[string]TreeNode{}
	collectTreeNodes(nodes, tree.Children)
	if _, ok := nodes["参考资料/示例.md"]; !ok {
		t.Fatalf("tree missing Chinese file, got paths %#v", nodes)
	}

	file, err := svc.ReadFile(context.Background(), FileRef{
		SkillID: resp.SkillID,
		RefType: "head",
		Path:    "参考资料/示例.md",
	})
	if err != nil {
		t.Fatalf("ReadFile Chinese path returned error: %v", err)
	}
	if !strings.Contains(file.Content, "中文路径正文") {
		t.Fatalf("ReadFile Chinese path content = %q", file.Content)
	}
}

func newCreateSkillValidationService(t *testing.T, db *gorm.DB, uploadStore *fakeUploadStore) *SkillService {
	t.Helper()
	return NewSkillService(SkillServiceDeps{
		DB:          db,
		UploadStore: uploadStore,
		BlobStore:   NewBlobStore(db, NewLocalObjectStore(t.TempDir())),
		Clock:       fixedClock(),
	})
}

func validCreateSkillRequest(uploadID string) CreateSkillRequest {
	return CreateSkillRequest{
		OwnerUserID:    "user_001",
		OwnerUserName:  "张三",
		CreateUserID:   "user_001",
		CreateUserName: "张三",
		Name:           "论文精读",
		Category:       "research",
		Description:    "用于阅读和总结论文的技能",
		Tags:           []string{"paper", "research"},
		AutoEvo:        false,
		IsEnabled:      boolPtr(true),
		Source: SourceInput{
			Type:     "uploaded_zip",
			UploadID: uploadID,
			Filename: "skill.zip",
		},
	}
}

func assertNoSkillTruthRows(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, table := range []string{"skills", "skill_revisions", "skill_revision_entries", "skill_drafts", "skill_draft_entries"} {
		if got := countRows(t, db, table, ""); got != 0 {
			t.Fatalf("%s count = %d, want 0", table, got)
		}
	}
}
