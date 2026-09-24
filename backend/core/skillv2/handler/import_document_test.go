package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"testing"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"lazymind/core/common"
	skillhttperr "lazymind/core/skillv2/httperr"
	skillmetadata "lazymind/core/skillv2/metadata"
	skillservice "lazymind/core/skillv2/service"
	"lazymind/core/skillv2/testutil"
)

func TestSkillHubImportNormalizesDocumentFilename(t *testing.T) {
	const sourceURL = "https://skillhub.cn/skills/example/filename-fixture"
	const document = "---\nname: filename-fixture\ndescription: A synthetic import fixture.\ncustom:\n  nested: [one, two]\n---\n# Fixture\n\nKeep the original body.\n"
	for _, entryPath := range []string{"SKILL.md", "skill.md", "Skill.Md", "wrapped/skill.md"} {
		t.Run(entryPath, func(t *testing.T) {
			var request createSkillRequest
			if err := json.Unmarshal([]byte(`{"source":{"type":"url","url":"`+sourceURL+`"}}`), &request); err != nil {
				t.Fatal(err)
			}
			source, cleanup, err := createSkillSourceFromRequest(context.Background(), "", "", "", "", nil, request.Source)
			if err != nil {
				t.Fatal(err)
			}
			if cleanup != nil {
				defer cleanup()
			}
			contents := map[string][]byte{
				"SKILL.md":            []byte(document),
				"references/notes.md": []byte("# Reference\nDo not change this.\n"),
				"scripts/run.py":      []byte("# Synthetic fixture; never executed.\n"),
			}
			archiveFiles := map[string][]byte{entryPath: contents["SKILL.md"]}
			for filePath, data := range contents {
				if filePath != "SKILL.md" {
					archiveFiles[path.Join(path.Dir(entryPath), filePath)] = data
				}
			}
			zipPath, err := writeSkillPackageZip(archiveFiles)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(zipPath)
			db := testutil.NewTestDB(t)
			downloader := &recordingZipDownloader{path: zipPath}
			svc := skillservice.NewSkillService(skillservice.SkillServiceDeps{
				DB: db.DB, Downloader: downloader,
				BlobStore: skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(t.TempDir())),
			})
			req := skillservice.CreateSkillRequest{OwnerUserID: "user_001", CreateUserID: "user_001", Source: source}
			response, err := svc.CreateSkill(context.Background(), req)
			if err != nil {
				t.Fatalf("SkillHub import: %v", err)
			}
			if downloader.calls != 1 || downloader.gotURL != "https://api.skillhub.cn/api/v1/download?slug=%40example%2Ffilename-fixture" {
				t.Fatalf("unexpected SkillHub download: %#v", downloader)
			}
			var skill testutil.SkillRow
			if err := db.Where("id = ?", response.SkillID).Take(&skill).Error; err != nil {
				t.Fatal(err)
			}
			if skill.SkillName != "filename-fixture" || skill.RelativeRoot != "external/filename-fixture" || skill.SkillMDPath != "SKILL.md" || skill.Description != "A synthetic import fixture." {
				t.Fatalf("persisted identity or metadata drifted: %#v", skill)
			}
			var revision testutil.SkillRevisionRow
			if err := db.Where("id = ?", response.HeadRevisionID).Take(&revision).Error; err != nil {
				t.Fatal(err)
			}
			if revision.SourceRefID != sourceURL || revision.SourceRefType != "url" {
				t.Fatalf("source reference changed: %#v", revision)
			}
			var entries []testutil.SkillRevisionEntryRow
			if err := db.Where("revision_id = ? AND entry_type = ?", response.HeadRevisionID, "file").Find(&entries).Error; err != nil {
				t.Fatal(err)
			}
			if len(entries) != len(contents) {
				t.Fatalf("persisted files = %d, want %d", len(entries), len(contents))
			}
			for _, entry := range entries {
				var blob testutil.SkillBlobRow
				if err := db.Where("hash = ?", entry.BlobHash).Take(&blob).Error; err != nil {
					t.Fatal(err)
				}
				want, ok := contents[entry.Path]
				if !ok || !bytes.Equal(blob.Content, want) {
					t.Fatalf("unexpected persisted file %q: %q", entry.Path, blob.Content)
				}
			}
			if _, err := svc.CreateSkill(context.Background(), req); !isImportIdentityConflict(err) {
				t.Fatalf("repeat import must reject an identity collision: %v", err)
			}
			if got := testutil.CountRows(t, db, "skills", ""); got != 1 {
				t.Fatalf("skills after collision = %d, want 1", got)
			}
		})
	}
}

func TestNormalizationWarningsDTO(t *testing.T) {
	warnings := []skillmetadata.NormalizationWarning{
		{Code: skillmetadata.NormalizationDescriptionCompacted, Message: "description warning"},
		{Code: skillmetadata.NormalizationCanonicalName, Message: "name warning"},
	}
	got := normalizationWarningsDTO(warnings)
	if len(got) != 2 || got[0]["code"] != warnings[0].Code || got[0]["message"] != warnings[0].Message || got[1]["code"] != warnings[1].Code || got[1]["message"] != warnings[1].Message {
		t.Fatalf("warning response = %#v", got)
	}
	if empty := normalizationWarningsDTO(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("empty warning response = %#v", empty)
	}
}

func TestSkillHubImportRecoversPlainDescriptionAndUsesCanonicalSlug(t *testing.T) {
	tests := []struct {
		name, pageURL, path, document, wantName, wantDescription string
	}{
		{
			name:            "plain description containing colon",
			pageURL:         "https://skillhub.cn/skills/clawhub_example/colon-description",
			path:            "SKILL.md",
			document:        "---\nname: colon-description\ndescription: Analyze discussions for: useful patterns.\ncustom: retained\n---\n# Original body\n",
			wantName:        "colon-description",
			wantDescription: "Analyze discussions for: useful patterns.",
		},
		{
			name:            "missing name in lowercase document",
			pageURL:         "https://skillhub.cn/skills/clawhub_example/clawsec",
			path:            "skill.md",
			document:        "# ClawSec\n\nA safe local status check.\n",
			wantName:        "clawsec",
			wantDescription: "A safe local status check.",
		},
		{
			name:            "authored name overrides source slug",
			pageURL:         "https://skillhub.cn/skills/clawhub_example/catalog-slug",
			path:            "SKILL.md",
			document:        "---\nname: authored-name\ndescription: An authored identity.\n---\n# Original body\n",
			wantName:        "authored-name",
			wantDescription: "An authored identity.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request createSkillRequest
			payload, err := json.Marshal(map[string]any{"source": map[string]any{"type": "url", "url": tt.pageURL}})
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(payload, &request); err != nil {
				t.Fatal(err)
			}
			source, cleanup, err := createSkillSourceFromRequest(context.Background(), "", "", "", "", nil, request.Source)
			if err != nil {
				t.Fatal(err)
			}
			if cleanup != nil {
				defer cleanup()
			}
			zipPath, err := writeSkillPackageZip(map[string][]byte{tt.path: []byte(tt.document)})
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(zipPath)
			db := testutil.NewTestDB(t)
			downloader := &recordingZipDownloader{path: zipPath}
			svc := skillservice.NewSkillService(skillservice.SkillServiceDeps{
				DB: db.DB, Downloader: downloader,
				BlobStore: skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(t.TempDir())),
			})
			response, err := svc.CreateSkill(context.Background(), skillservice.CreateSkillRequest{
				OwnerUserID: "user_001", CreateUserID: "user_001", Source: source,
			})
			if err != nil {
				t.Fatal(err)
			}
			var row testutil.SkillRow
			if err := db.Where("id = ?", response.SkillID).Take(&row).Error; err != nil {
				t.Fatal(err)
			}
			if row.SkillName != tt.wantName || row.RelativeRoot != "external/"+tt.wantName || row.Description != tt.wantDescription {
				t.Fatalf("persisted metadata drifted: %#v", row)
			}
			file, err := svc.ReadFile(context.Background(), skillservice.FileRef{SkillID: response.SkillID, RefType: "head", Path: "SKILL.md"})
			if err != nil {
				t.Fatal(err)
			}
			body := tt.document[strings.Index(tt.document, "# "):]
			if !strings.HasSuffix(file.Content, body) {
				t.Fatalf("document body changed: %q", file.Content)
			}
			effective, err := skillmetadata.EffectiveDocument([]byte(file.Content), row.SkillName, row.Description)
			if err != nil {
				t.Fatal(err)
			}
			runtimeMeta, err := skillmetadata.ParseRequired(effective)
			if err != nil || runtimeMeta.Name != row.SkillName {
				t.Fatalf("runtime identity drifted: %#v, %v", runtimeMeta, err)
			}
		})
	}
}

func TestSkillHubImportNormalizesLongDescriptionAndUnsafeDisplayName(t *testing.T) {
	tests := []struct {
		name, pageURL, document, wantName, wantOriginalField, wantOriginalValue, warningCode string
	}{
		{
			name:              "long unicode description",
			pageURL:           "https://skillhub.cn/skills/clawhub_example/who-is-actor",
			document:          "---\nname: who-is-actor\ndescription: >-\n  " + strings.Repeat("Privacy: analyze only an explicitly authorized repository. 隐私保护。", 40) + "\n---\n# Who Is Actor\n\nFull operating instructions remain here.\n",
			wantName:          "who-is-actor",
			wantOriginalField: skillmetadata.OriginalDescriptionField,
			warningCode:       skillmetadata.NormalizationDescriptionCompacted,
		},
		{
			name:              "unsafe display name uses verified source slug",
			pageURL:           "https://skillhub.cn/skills/org-28ib33ph/lingyi-user-persona-and-crowd-insight",
			document:          "---\nname: 用户画像与人群洞察 / User Persona & Crowd Insight\ndescription: Build a structured persona from supplied data.\ncustom: retained\n---\n# Persona\n",
			wantName:          "lingyi-user-persona-and-crowd-insight",
			wantOriginalField: skillmetadata.OriginalNameField,
			wantOriginalValue: "用户画像与人群洞察 / User Persona & Crowd Insight",
			warningCode:       skillmetadata.NormalizationCanonicalName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zipPath, err := writeSkillPackageZip(map[string][]byte{"SKILL.md": []byte(tt.document), "references/keep.md": []byte("retained")})
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(zipPath)
			db := testutil.NewTestDB(t)
			svc := skillservice.NewSkillService(skillservice.SkillServiceDeps{
				DB: db.DB, Downloader: &recordingZipDownloader{path: zipPath},
				BlobStore: skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(t.TempDir())),
			})
			var request createSkillRequest
			payload, _ := json.Marshal(map[string]any{"source": map[string]any{"type": "url", "url": tt.pageURL}})
			if err := json.Unmarshal(payload, &request); err != nil {
				t.Fatal(err)
			}
			source, cleanup, err := createSkillSourceFromRequest(context.Background(), "", "", "", "", nil, request.Source)
			if err != nil {
				t.Fatal(err)
			}
			if cleanup != nil {
				defer cleanup()
			}
			response, err := svc.CreateSkill(context.Background(), skillservice.CreateSkillRequest{OwnerUserID: "user_001", CreateUserID: "user_001", Source: source})
			if err != nil {
				t.Fatal(err)
			}
			if len(response.Warnings) != 1 || response.Warnings[0].Code != tt.warningCode {
				t.Fatalf("warnings = %#v", response.Warnings)
			}
			var row testutil.SkillRow
			if err := db.Where("id = ?", response.SkillID).Take(&row).Error; err != nil {
				t.Fatal(err)
			}
			if row.SkillName != tt.wantName || row.RelativeRoot != "external/"+tt.wantName || utf8.RuneCountInString(row.Description) > skillmetadata.MaxSkillDescriptionLength {
				t.Fatalf("persisted runtime identity/description = %#v", row)
			}
			file, err := svc.ReadFile(context.Background(), skillservice.FileRef{SkillID: response.SkillID, RefType: "head", Path: "SKILL.md"})
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			normalized := strings.ReplaceAll(file.Content, "\r\n", "\n")
			rest := strings.TrimPrefix(normalized, "---\n")
			end := strings.Index(rest, "\n---")
			if end < 0 || yaml.Unmarshal([]byte(rest[:end]), &document) != nil {
				t.Fatalf("invalid persisted document: %q", file.Content)
			}
			wantOriginal := tt.wantOriginalValue
			if wantOriginal == "" {
				var original map[string]any
				originalRest := strings.TrimPrefix(tt.document, "---\n")
				originalEnd := strings.Index(originalRest, "\n---")
				if err := yaml.Unmarshal([]byte(originalRest[:originalEnd]), &original); err != nil {
					t.Fatal(err)
				}
				wantOriginal = original["description"].(string)
			}
			if document[tt.wantOriginalField] != wantOriginal || document[skillmetadata.NormalizationField] == nil {
				t.Fatalf("original metadata or warning marker not preserved: %#v", document)
			}
			if _, ok := document["custom"]; tt.wantOriginalField == skillmetadata.OriginalNameField && !ok {
				t.Fatal("unknown metadata was lost")
			}
			ref, err := svc.ReadFile(context.Background(), skillservice.FileRef{SkillID: response.SkillID, RefType: "head", Path: "references/keep.md"})
			if err != nil || ref.Content != "retained" {
				t.Fatalf("reference not preserved: %#v, %v", ref, err)
			}
			if _, err := svc.CreateSkill(context.Background(), skillservice.CreateSkillRequest{OwnerUserID: "user_001", CreateUserID: "user_001", Source: source}); !isImportIdentityConflict(err) {
				t.Fatalf("canonical identity collision must reject repeat import: %v", err)
			}
		})
	}
}

func TestSkillHubFallbackNameCollisionIsRejected(t *testing.T) {
	zipPath, err := writeSkillPackageZip(map[string][]byte{"skill.md": []byte("# Shared Skill\n\nA local analysis task.\n")})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(zipPath)
	db := testutil.NewTestDB(t)
	downloader := &recordingZipDownloader{path: zipPath}
	svc := skillservice.NewSkillService(skillservice.SkillServiceDeps{
		DB: db.DB, Downloader: downloader,
		BlobStore: skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(t.TempDir())),
	})
	for i, pageURL := range []string{
		"https://skillhub.cn/skills/first/shared-skill",
		"https://skillhub.cn/skills/second/shared-skill",
	} {
		var request createSkillRequest
		payload, err := json.Marshal(map[string]any{"source": map[string]any{"type": "url", "url": pageURL}})
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			t.Fatal(err)
		}
		source, cleanup, err := createSkillSourceFromRequest(context.Background(), "", "", "", "", nil, request.Source)
		if err != nil {
			t.Fatal(err)
		}
		if cleanup != nil {
			defer cleanup()
		}
		_, err = svc.CreateSkill(context.Background(), skillservice.CreateSkillRequest{OwnerUserID: "user_001", CreateUserID: "user_001", Source: source})
		if i == 0 && err != nil {
			t.Fatal(err)
		}
		if i == 1 && !isImportIdentityConflict(err) {
			t.Fatalf("second namespace must hit stable slug collision: %v", err)
		}
	}
	if got := testutil.CountRows(t, db, "skills", ""); got != 1 {
		t.Fatalf("skills after collision = %d, want 1", got)
	}
}

func isImportIdentityConflict(err error) bool {
	if err == nil {
		return false
	}
	switch skillhttperr.ForError(err).Code {
	case "path_exists", "skill_already_exists":
		return true
	default:
		return false
	}
}

func TestSkillHubFallbackNameRejectsUnsupportedRuntimePath(t *testing.T) {
	const pageURL = "https://skillhub.cn/skills/example/bad%20name"
	var request createSkillRequest
	if err := json.Unmarshal([]byte(`{"source":{"type":"url","url":"`+pageURL+`"}}`), &request); err != nil {
		t.Fatal(err)
	}
	source, cleanup, err := createSkillSourceFromRequest(context.Background(), "", "", "", "", nil, request.Source)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	zipPath, err := writeSkillPackageZip(map[string][]byte{"skill.md": []byte("# Skill\n\nA local task.\n")})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(zipPath)
	db := testutil.NewTestDB(t)
	svc := skillservice.NewSkillService(skillservice.SkillServiceDeps{
		DB: db.DB, Downloader: &recordingZipDownloader{path: zipPath},
		BlobStore: skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(t.TempDir())),
	})
	_, err = svc.CreateSkill(context.Background(), skillservice.CreateSkillRequest{OwnerUserID: "user_001", CreateUserID: "user_001", Source: source})
	if err == nil || skillhttperr.ForError(err).Code != "invalid_skill_name" {
		t.Fatalf("unsupported runtime slug must fail with invalid_skill_name: %v", err)
	}
	if got := testutil.CountRows(t, db, "skills", ""); got != 0 {
		t.Fatalf("skills after rejected slug = %d, want 0", got)
	}
}

func TestImportDocumentErrorsAreActionable(t *testing.T) {
	const valid = "---\nname: fixture\ndescription: Synthetic fixture.\n---\n# Fixture\n"
	tests := []struct {
		name  string
		files map[string][]byte
		code  string
	}{
		{"missing", map[string][]byte{"README.md": []byte("No skill.")}, "skill_md_not_found"},
		{"ambiguous", map[string][]byte{"SKILL.md": []byte(valid), "skill.md": []byte(valid)}, "skill_md_ambiguous"},
		{"case variants", map[string][]byte{"Skill.md": []byte(valid), "skill.MD": []byte(valid)}, "skill_md_ambiguous"},
		{"directory collision", map[string][]byte{"skill.md": []byte(valid), "SKILL.md/notes.md": []byte("Collision.")}, "skill_md_ambiguous"},
		{"nested without root", map[string][]byte{"README.md": []byte("Root"), "nested/skill.md": []byte(valid)}, "skill_md_not_found"},
		{"yaml", map[string][]byte{"skill.md": []byte("---\nname: fixture\ndescription: text: ambiguous # possible comment\n---\n")}, "frontmatter_yaml_invalid"},
		{"structured name", map[string][]byte{"skill.md": []byte("---\nname: [one, two]\ndescription: Synthetic fixture.\n---\n")}, "frontmatter_yaml_invalid"},
		{"path name", map[string][]byte{"skill.md": []byte("---\nname: ../escape\ndescription: Synthetic fixture.\n---\n")}, "invalid_skill_name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zipPath, err := writeSkillPackageZip(tt.files)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(zipPath)
			archive, err := os.ReadFile(zipPath)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/zip")
				_, _ = w.Write(archive)
			}))
			defer server.Close()
			db := testutil.NewTestDB(t)
			withHandlerDB(t, db)
			payload, err := json.Marshal(map[string]any{"source": map[string]any{"type": "url", "url": server.URL + "/fixture.zip"}})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/core/skills", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-User-Id", "user_001")
			rec := httptest.NewRecorder()
			Create(rec, req)
			if rec.Code < 400 || rec.Code >= 500 {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var response common.APIResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			data, ok := response.Data.(map[string]any)
			if !ok || data["code"] != tt.code {
				t.Errorf("diagnostic = %#v, want %q; body = %s", response.Data, tt.code, rec.Body.String())
			}
			for _, table := range []string{"skills", "skill_revisions", "skill_blobs"} {
				if got := testutil.CountRows(t, db, table, ""); got != 0 {
					t.Errorf("%s rows after rejection = %d", table, got)
				}
			}
		})
	}
}
