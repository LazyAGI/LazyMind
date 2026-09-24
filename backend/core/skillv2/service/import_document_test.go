package service

import (
	"bytes"
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	skillmetadata "lazymind/core/skillv2/metadata"
	skillsourceurl "lazymind/core/skillv2/sourceurl"
)

func TestExternalDocumentNormalizationPreservesStrictLocalCreation(t *testing.T) {
	for _, sourceType := range []string{"uploaded_zip", "local_zip"} {
		t.Run(sourceType, func(t *testing.T) {
			db := newSkillV2TestDB(t)
			document := externalSkillMD("fixture", "Synthetic fixture.")
			zipPath := filepath.Join(t.TempDir(), "fixture.zip")
			writeSkillZip(t, zipPath, map[string][]byte{"skill.md": document})
			uploads := newFakeUploadStore()
			uploads.Put(UploadSession{UploadID: "upload", OwnerUserID: "user_001", State: "completed", StoredPath: zipPath, Filename: "fixture.zip"})
			svc := newCreateSkillValidationService(t, db, uploads)
			response, err := svc.CreateSkill(context.Background(), CreateSkillRequest{
				OwnerUserID: "user_001", CreateUserID: "user_001",
				Name: "fixture", Category: "external", Description: "Synthetic fixture.",
				Source: SourceInput{Type: sourceType, UploadID: "upload", StoredPath: zipPath, Filename: "fixture.zip"},
			})
			if sourceType == "local_zip" {
				if err == nil {
					t.Fatal("local creation unexpectedly normalized a noncanonical document")
				}
				assertNoSkillTruthRows(t, db)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if blob := getBlobByPath(t, db, response.HeadRevisionID, "SKILL.md"); !bytes.Equal(blob.Content, document) {
				t.Fatalf("stored document changed: %q", blob.Content)
			}
		})
	}
}

func TestLocalSourceReplacementRemainsStrict(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "replace-target", "replace-rev1")
	zipPath := filepath.Join(t.TempDir(), "local-replacement.zip")
	writeSkillZip(t, zipPath, map[string][]byte{"skill.md": externalSkillMD("replacement", "Local replacement.")})
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})
	if _, err := svc.PatchSkill(context.Background(), PatchSkillRequest{
		SkillID: "replace-target", UserID: "user_001",
		Source: &SourceInput{Type: "local_zip", StoredPath: zipPath, Filename: "local-replacement.zip"},
	}); err == nil {
		t.Fatal("local source replacement unexpectedly normalized lowercase skill.md")
	}
	assertHeadRevisionUnchanged(t, db, "replace-target", "replace-rev1")
}

func TestExternalSourceReplacementMatchesCreateNormalization(t *testing.T) {
	tests := []struct {
		name, pageURL, entryPath, document, wantName, wantDescription string
		wantWarnings                                                  []string
	}{
		{
			name:      "lowercase document",
			pageURL:   "https://skillhub.cn/skills/example/lowercase-replacement",
			entryPath: "skill.md",
			document:  "---\nname: lowercase-replacement\ndescription: Canonical lowercase replacement.\n---\n# Lowercase\n",
			wantName:  "lowercase-replacement", wantDescription: "Canonical lowercase replacement.",
		},
		{
			name:      "recoverable description yaml",
			pageURL:   "https://skillhub.cn/skills/example/yaml-replacement",
			entryPath: "SKILL.md",
			document:  "---\nname: yaml-replacement\ndescription: Analyze discussions for: useful patterns.\ncustom: retained\n---\n# YAML\n",
			wantName:  "yaml-replacement", wantDescription: "Analyze discussions for: useful patterns.",
		},
		{
			name:      "canonical name and long description",
			pageURL:   "https://skillhub.cn/skills/example/persona-replacement",
			entryPath: "SKILL.md",
			document:  "---\nname: Persona / Crowd Insight\ndescription: >-\n  " + strings.Repeat("Privacy: use only supplied fictional data. ", 40) + "\nunknown: retained\n---\n# Persona\n",
			wantName:  "persona-replacement",
			wantWarnings: []string{
				skillmetadata.NormalizationDescriptionCompacted,
				skillmetadata.NormalizationCanonicalName,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newSkillV2TestDB(t)
			seedSkillWithHeadRevision(t, db, "replace-target", "replace-rev1")
			if err := db.Model(&testSkillV2SkillRow{}).Where("id = ?", "replace-target").Update("owner_user_id", "user_002").Error; err != nil {
				t.Fatal(err)
			}
			zipPath := filepath.Join(t.TempDir(), "replacement.zip")
			writeSkillZip(t, zipPath, map[string][]byte{
				tt.entryPath:      []byte(tt.document),
				"references/a.md": []byte("retained"),
			})
			source := verifiedSkillHubSource(t, tt.pageURL)
			svc := NewSkillService(SkillServiceDeps{
				DB: db, Downloader: NewFakeZipDownloader(map[string]string{source.URL: zipPath}),
				BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock(),
			})

			created, err := svc.CreateSkill(context.Background(), CreateSkillRequest{
				OwnerUserID: "user_001", CreateUserID: "user_001", Source: source,
			})
			if err != nil {
				t.Fatalf("CreateSkill: %v", err)
			}
			replaced, err := svc.PatchSkill(context.Background(), PatchSkillRequest{
				SkillID: "replace-target", UserID: "user_002", Source: &source,
			})
			if err != nil {
				t.Fatalf("PatchSkill source replacement: %v", err)
			}
			if got := normalizationWarningCodes(created.Warnings); !equalStrings(got, tt.wantWarnings) {
				t.Fatalf("CreateSkill warnings = %v, want %v", got, tt.wantWarnings)
			}
			if got := normalizationWarningCodes(replaced.Warnings); !equalStrings(got, tt.wantWarnings) {
				t.Fatalf("PatchSkill warnings = %v, want %v", got, tt.wantWarnings)
			}

			var createRow, replaceRow testSkillV2SkillRow
			if err := db.Where("id = ?", created.SkillID).Take(&createRow).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Where("id = ?", "replace-target").Take(&replaceRow).Error; err != nil {
				t.Fatal(err)
			}
			if createRow.SkillName != replaceRow.SkillName || createRow.Category != replaceRow.Category || createRow.Description != replaceRow.Description || createRow.RelativeRoot != replaceRow.RelativeRoot {
				t.Fatalf("create/replace metadata differ: create=%#v replace=%#v", createRow, replaceRow)
			}
			if replaceRow.SkillName != tt.wantName || replaceRow.Category != skillmetadata.ExternalCategory || replaceRow.RelativeRoot != "external/"+tt.wantName {
				t.Fatalf("replacement identity = %#v", replaceRow)
			}
			if tt.wantDescription != "" && replaceRow.Description != tt.wantDescription {
				t.Fatalf("replacement description = %q", replaceRow.Description)
			}
			if utf8.RuneCountInString(replaceRow.Description) > skillmetadata.MaxSkillDescriptionLength {
				t.Fatalf("replacement description length = %d", utf8.RuneCountInString(replaceRow.Description))
			}

			createDoc := getBlobByPath(t, db, created.HeadRevisionID, "SKILL.md").Content
			replaceDoc := getBlobByPath(t, db, replaced.HeadRevisionID, "SKILL.md").Content
			if !bytes.Equal(createDoc, replaceDoc) {
				t.Fatalf("create/replace canonical documents differ:\ncreate=%s\nreplace=%s", createDoc, replaceDoc)
			}
			if _, err := skillmetadata.ParseRequired(replaceDoc); err != nil {
				t.Fatalf("persisted replacement document is not strict: %v", err)
			}
			if entries := listRevisionEntries(t, db, replaced.HeadRevisionID); entries["SKILL.md"].Path != "SKILL.md" || entries["skill.md"].Path != "" {
				t.Fatalf("replacement document paths = %#v", entries)
			}
			if ref := getBlobByPath(t, db, replaced.HeadRevisionID, "references/a.md"); string(ref.Content) != "retained" {
				t.Fatalf("replacement reference = %q", ref.Content)
			}
		})
	}
}

func normalizationWarningCodes(warnings []skillmetadata.NormalizationWarning) []string {
	codes := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		codes = append(codes, warning.Code)
	}
	return codes
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestExternalSourceReplacementRetainsStrictRejections(t *testing.T) {
	valid := externalSkillMD("replacement", "Valid replacement.")
	tests := []struct {
		name  string
		files map[string][]byte
	}{
		{"ambiguous documents", map[string][]byte{"SKILL.md": valid, "skill.md": valid}},
		{"path traversal", map[string][]byte{"SKILL.md": valid, "../escape.txt": []byte("escape")}},
		{"invalid name without source slug", map[string][]byte{"SKILL.md": []byte("---\nname: Display / Name\ndescription: Invalid identity.\n---\n# Invalid\n")}},
		{"unrecoverable yaml", map[string][]byte{"SKILL.md": []byte("---\nname: replacement\ndescription: text: ambiguous # comment\n---\n# Invalid\n")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newSkillV2TestDB(t)
			seedSkillWithHeadRevision(t, db, "replace-target", "replace-rev1")
			zipPath := filepath.Join(t.TempDir(), "replacement.zip")
			writeSkillZip(t, zipPath, tt.files)
			uploads := newFakeUploadStore()
			uploads.Put(UploadSession{UploadID: "replace", OwnerUserID: "user_001", State: "completed", StoredPath: zipPath, Filename: "replacement.zip"})
			svc := NewSkillService(SkillServiceDeps{DB: db, UploadStore: uploads, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})
			if _, err := svc.PatchSkill(context.Background(), PatchSkillRequest{SkillID: "replace-target", UserID: "user_001", Source: &SourceInput{Type: "uploaded_zip", UploadID: "replace"}}); err == nil {
				t.Fatal("invalid external replacement unexpectedly succeeded")
			}
			assertHeadRevisionUnchanged(t, db, "replace-target", "replace-rev1")
			if got := countRows(t, db, "skill_revisions", "skill_id = ?", "replace-target"); got != 1 {
				t.Fatalf("replacement revisions after rejection = %d", got)
			}
		})
	}
}

func TestExternalSourceReplacementRejectsIdentityCollision(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "replace-target", "replace-rev1")
	zipPath := filepath.Join(t.TempDir(), "taken.zip")
	writeSkillZip(t, zipPath, map[string][]byte{"SKILL.md": externalSkillMD("taken-name", "Taken identity.")})
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})
	if _, err := svc.CreateSkill(context.Background(), CreateSkillRequest{
		OwnerUserID: "user_001", CreateUserID: "user_001", Name: "taken-name", Category: skillmetadata.ExternalCategory, Description: "Taken identity.",
		Source: SourceInput{Type: "local_zip", StoredPath: zipPath, Filename: "taken.zip"},
	}); err != nil {
		t.Fatal(err)
	}
	uploads := newFakeUploadStore()
	uploads.Put(UploadSession{UploadID: "replace", OwnerUserID: "user_001", State: "completed", StoredPath: zipPath, Filename: "taken.zip"})
	svc.uploadStore = uploads
	if _, err := svc.PatchSkill(context.Background(), PatchSkillRequest{SkillID: "replace-target", UserID: "user_001", Source: &SourceInput{Type: "uploaded_zip", UploadID: "replace"}}); err == nil {
		t.Fatal("identity collision unexpectedly succeeded")
	}
	assertHeadRevisionUnchanged(t, db, "replace-target", "replace-rev1")
}

func verifiedSkillHubSource(t *testing.T, pageURL string) SourceInput {
	t.Helper()
	parsed, err := url.Parse(pageURL)
	if err != nil {
		t.Fatal(err)
	}
	resolution, matched, err := skillsourceurl.ResolveSkillHubPageURL(parsed)
	if err != nil || !matched {
		t.Fatalf("resolve SkillHub URL: matched=%v err=%v", matched, err)
	}
	return SourceInput{Type: "url", URL: resolution.DownloadURL, SourceURL: pageURL}
}
