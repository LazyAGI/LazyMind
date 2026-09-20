package service

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
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
