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

func TestCreateSkillFromUploadedZip_ResolvesMissingFrontmatterMetadata(t *testing.T) {
	tests := []struct {
		name            string
		filename        string
		entryPath       string
		content         []byte
		wantName        string
		wantDescription string
		wantGenerated   bool
	}{
		{
			name:            "missing frontmatter at archive root",
			filename:        "frontmatter.zip",
			entryPath:       "SKILL.md",
			content:         []byte("# Skill\n\n正文首段。\n"),
			wantName:        "frontmatter",
			wantDescription: "正文首段。",
		},
		{
			name:            "missing name uses package root",
			filename:        "archive-name.zip",
			entryPath:       "package-root/SKILL.md",
			content:         []byte("---\ndescription: description\n---\n# Skill\n"),
			wantName:        "package-root",
			wantDescription: "description",
		},
		{
			name:            "invalid package root uses archive filename",
			filename:        "archive-name.zip",
			entryPath:       " /SKILL.md",
			content:         []byte("---\ndescription: description\n---\n# Skill\n"),
			wantName:        "archive-name",
			wantDescription: "description",
		},
		{
			name:            "missing package root and archive filename uses generated id",
			entryPath:       "SKILL.md",
			content:         []byte("---\ndescription: description\n---\n# Skill\n"),
			wantDescription: "description",
			wantGenerated:   true,
		},
		{
			name:            "missing description uses first body paragraph",
			filename:        "skill.zip",
			entryPath:       "SKILL.md",
			content:         []byte("---\nname: skill\n---\n# Skill\n\n第一段。\n继续第一段。\n\n第二段。\n"),
			wantName:        "skill",
			wantDescription: "第一段。 继续第一段。",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := newSkillV2TestDB(t)
			storedFilename := tc.filename
			if storedFilename == "" {
				storedFilename = "stored.zip"
			}
			zipPath := filepath.Join(t.TempDir(), storedFilename)
			writeSkillZip(t, zipPath, map[string][]byte{tc.entryPath: tc.content})
			uploadStore := newFakeUploadStore()
			uploadID := "upload_" + strings.ReplaceAll(tc.name, " ", "_")
			uploadStore.Put(UploadSession{UploadID: uploadID, OwnerUserID: "user_001", State: "completed", StoredPath: zipPath, Filename: tc.filename})
			svc := newCreateSkillValidationService(t, db, uploadStore)

			resp, err := svc.CreateSkill(context.Background(), validCreateSkillRequest(uploadID))
			if err != nil {
				t.Fatalf("CreateSkill returned error: %v", err)
			}
			wantName := tc.wantName
			if tc.wantGenerated {
				wantName = "lazymind-skill-" + resp.SkillID
			}
			if resp.Name != wantName || resp.Description != tc.wantDescription {
				t.Fatalf("CreateSkill metadata = (%q, %q), want (%q, %q)", resp.Name, resp.Description, wantName, tc.wantDescription)
			}
			var skill testSkillV2SkillRow
			if err := db.Where("id = ?", resp.SkillID).Take(&skill).Error; err != nil {
				t.Fatalf("query created skill: %v", err)
			}
			if skill.SkillName != wantName || skill.Description != tc.wantDescription {
				t.Fatalf("stored metadata = (%q, %q), want (%q, %q)", skill.SkillName, skill.Description, wantName, tc.wantDescription)
			}
			blob := getBlobByPath(t, db, resp.HeadRevisionID, "SKILL.md")
			if string(blob.Content) != string(tc.content) {
				t.Fatalf("stored SKILL.md changed: got %q want %q", blob.Content, tc.content)
			}
		})
	}
}

func TestCreateSkillFromUploadedZip_RejectsInvalidExistingFrontmatter(t *testing.T) {
	for name, content := range map[string][]byte{
		"malformed yaml": []byte("---\nname: [broken\n---\n正文。\n"),
		"invalid name":   []byte("---\nname: bad/name\ndescription: description\n---\n正文。\n"),
	} {
		t.Run(name, func(t *testing.T) {
			db := newSkillV2TestDB(t)
			zipPath := filepath.Join(t.TempDir(), "skill.zip")
			writeSkillZip(t, zipPath, map[string][]byte{"SKILL.md": content})
			uploadStore := newFakeUploadStore()
			uploadStore.Put(UploadSession{UploadID: "upload_invalid", OwnerUserID: "user_001", State: "completed", StoredPath: zipPath, Filename: "skill.zip"})
			svc := newCreateSkillValidationService(t, db, uploadStore)

			if _, err := svc.CreateSkill(context.Background(), validCreateSkillRequest("upload_invalid")); err == nil {
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
