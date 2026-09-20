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

	"lazymind/core/common"
	skillhttperr "lazymind/core/skillv2/httperr"
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
			if _, err := svc.CreateSkill(context.Background(), req); err == nil || skillhttperr.ForError(err).Code != "path_exists" {
				t.Fatalf("repeat import must reject an identity collision: %v", err)
			}
			if got := testutil.CountRows(t, db, "skills", ""); got != 1 {
				t.Fatalf("skills after collision = %d, want 1", got)
			}
		})
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
		{"yaml", map[string][]byte{"skill.md": []byte("---\nname: fixture\ndescription: text: ambiguous\n---\n")}, "frontmatter_yaml_invalid"},
		{"structured name", map[string][]byte{"skill.md": []byte("---\nname: [one, two]\ndescription: Synthetic fixture.\n---\n")}, "frontmatter_yaml_invalid"},
		{"path name", map[string][]byte{"skill.md": []byte("---\nname: ../escape\ndescription: Synthetic fixture.\n---\n")}, "invalid_skill_name"},
		{"long description", map[string][]byte{"skill.md": []byte("---\nname: fixture\ndescription: " + strings.Repeat("x", 1025) + "\n---\n")}, "description_too_long"},
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
