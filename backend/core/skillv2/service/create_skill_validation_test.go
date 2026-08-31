package service

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	skillmetadata "lazymind/core/skillv2/metadata"
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

func TestCreateSkillFromUploadedZip_FallsBackMissingMetadataWithoutRewritingSkillMD(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		entryPath   string
		content     []byte
		wantName    string
		wantDesc    string
		generatedID bool
	}{
		{
			name:      "no frontmatter uses archive filename",
			filename:  "frontmatter.zip",
			entryPath: "SKILL.md",
			content:   []byte("# Skill\n\nFirst useful paragraph.\n\nSecond paragraph.\n"),
			wantName:  "frontmatter",
			wantDesc:  "First useful paragraph.",
		},
		{
			name:      "missing name uses package root",
			filename:  "ignored.zip",
			entryPath: "wrapped-skill/SKILL.md",
			content:   []byte("---\ndescription: Existing description\n---\n# Skill\n"),
			wantName:  "wrapped-skill",
			wantDesc:  "Existing description",
		},
		{
			name:      "missing description uses body",
			filename:  "ignored.zip",
			entryPath: "SKILL.md",
			content:   []byte("---\nname: existing-name\n---\n# Skill\n\nDescription from the body.\n"),
			wantName:  "existing-name",
			wantDesc:  "Description from the body.",
		},
		{
			name:        "no naming source uses generated id",
			entryPath:   "SKILL.md",
			content:     []byte("# Skill\n\nGenerated fallback description.\n"),
			wantDesc:    "Generated fallback description.",
			generatedID: true,
		},
		{
			name:      "complete frontmatter remains authoritative",
			filename:  "ignored.zip",
			entryPath: "SKILL.md",
			content:   externalSkillMD("source-name", "Source description"),
			wantName:  "source-name",
			wantDesc:  "Source description",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newSkillV2TestDB(t)
			zipPath := filepath.Join(t.TempDir(), "source.zip")
			writeSkillZip(t, zipPath, map[string][]byte{tt.entryPath: tt.content})
			uploadStore := newFakeUploadStore()
			uploadID := "upload_missing_metadata"
			uploadStore.Put(UploadSession{UploadID: uploadID, OwnerUserID: "user_001", State: "completed", StoredPath: zipPath, Filename: tt.filename})
			svc := newCreateSkillValidationService(t, db, uploadStore)

			resp, err := svc.CreateSkill(context.Background(), validCreateSkillRequest(uploadID))
			if err != nil {
				t.Fatalf("CreateSkill returned error: %v", err)
			}
			var skill testSkillV2SkillRow
			if err := db.Where("id = ?", resp.SkillID).Take(&skill).Error; err != nil {
				t.Fatalf("query skill: %v", err)
			}
			wantName := tt.wantName
			if tt.generatedID {
				wantName = "lazymind-skill-" + resp.SkillID
			}
			if skill.Category != skillmetadata.ExternalCategory || skill.SkillName != wantName || skill.Description != tt.wantDesc {
				t.Fatalf("skill metadata = %#v, want category=%q name=%q description=%q", skill, skillmetadata.ExternalCategory, wantName, tt.wantDesc)
			}
			blob := getBlobByPath(t, db, resp.HeadRevisionID, "SKILL.md")
			if !bytes.Equal(blob.Content, tt.content) {
				t.Fatalf("stored SKILL.md = %q, want original %q", blob.Content, tt.content)
			}
		})
	}
}

func TestCreateSkillFromUploadedZip_RejectsInvalidMetadata(t *testing.T) {
	for name, content := range map[string][]byte{
		"invalid yaml":     []byte("---\nname: [\n---\n# Skill\n"),
		"invalid name":     []byte("---\nname: bad/name\ndescription: description\n---\n# Skill\n"),
		"long name":        []byte("---\nname: " + strings.Repeat("a", skillmetadata.MaxSkillNameLength+1) + "\ndescription: description\n---\n# Skill\n"),
		"long description": []byte("---\nname: valid-name\ndescription: " + strings.Repeat("a", skillmetadata.MaxSkillDescriptionLength+1) + "\n---\n# Skill\n"),
	} {
		t.Run(name, func(t *testing.T) {
			db := newSkillV2TestDB(t)
			zipPath := filepath.Join(t.TempDir(), "invalid.zip")
			writeSkillZip(t, zipPath, map[string][]byte{"SKILL.md": content})
			uploadStore := newFakeUploadStore()
			uploadStore.Put(UploadSession{UploadID: "upload_invalid_metadata", OwnerUserID: "user_001", State: "completed", StoredPath: zipPath, Filename: "invalid.zip"})

			_, err := newCreateSkillValidationService(t, db, uploadStore).CreateSkill(context.Background(), validCreateSkillRequest("upload_invalid_metadata"))
			if err == nil {
				t.Fatal("CreateSkill succeeded")
			}
			assertNoSkillTruthRows(t, db)
		})
	}
}

func TestCreateSkill_RejectsTooLongMetadata(t *testing.T) {
	for _, sourceType := range []string{"uploaded_zip", "local_zip"} {
		for name, spec := range map[string]struct {
			skillName   string
			description string
		}{
			"name": {
				skillName:   strings.Repeat("a", skillmetadata.MaxSkillNameLength+1),
				description: "用于阅读和总结论文的技能",
			},
			"description": {
				skillName:   "论文精读",
				description: strings.Repeat("a", skillmetadata.MaxSkillDescriptionLength+1),
			},
		} {
			t.Run(sourceType+"/"+name, func(t *testing.T) {
				db := newSkillV2TestDB(t)
				zipPath := filepath.Join(t.TempDir(), name+".zip")
				writeSkillZip(t, zipPath, map[string][]byte{
					"SKILL.md": externalSkillMD(spec.skillName, spec.description),
				})
				uploadStore := newFakeUploadStore()
				req := validCreateSkillRequest("upload_too_long_" + name)
				req.Name = spec.skillName
				req.Description = spec.description
				if sourceType == "uploaded_zip" {
					uploadStore.Put(UploadSession{
						UploadID:    req.Source.UploadID,
						OwnerUserID: "user_001",
						State:       "completed",
						StoredPath:  zipPath,
						Filename:    name + ".zip",
					})
				} else {
					req.Source = SourceInput{
						Type:       "local_zip",
						StoredPath: zipPath,
						Filename:   name + ".zip",
					}
				}
				svc := newCreateSkillValidationService(t, db, uploadStore)

				_, err := svc.CreateSkill(context.Background(), req)
				if err == nil {
					t.Fatal("CreateSkill succeeded")
				}
				if !strings.Contains(err.Error(), "cannot exceed") {
					t.Fatalf("CreateSkill error = %v, want cannot exceed", err)
				}
				assertNoSkillTruthRows(t, db)
			})
		}
	}
}

func TestCreateSkillFromUploadedZip_AllowsSingleTopLevelDirectory(t *testing.T) {
	db := newSkillV2TestDB(t)
	zipPath := filepath.Join(t.TempDir(), "wrapped.zip")
	writeSkillZip(t, zipPath, map[string][]byte{
		"openclaw-openclaw-changelog-update/SKILL.md":            externalSkillMD("openclaw-openclaw-changelog-update", "OpenClaw changelog update"),
		"openclaw-openclaw-changelog-update/references/a.md":     []byte("# A\n"),
		"__MACOSX/openclaw-openclaw-changelog-update/._SKILL.md": []byte("macOS metadata"),
		"openclaw-openclaw-changelog-update/.DS_Store":           []byte("finder metadata"),
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
	if _, ok := entries["__MACOSX/openclaw-openclaw-changelog-update/._SKILL.md"]; ok {
		t.Fatal("revision entries kept macOS metadata")
	}
	if _, ok := entries[".DS_Store"]; ok {
		t.Fatal("revision entries kept Finder metadata")
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
