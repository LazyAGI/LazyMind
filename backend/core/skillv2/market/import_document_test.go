package market

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	skillmetadata "lazymind/core/skillv2/metadata"
	"lazymind/core/skillv2/testutil"
)

type verifiedPackageDownloader struct{ path, name string }

func (d verifiedPackageDownloader) Download(context.Context, string) (string, error) {
	panic("verified package metadata must not be discarded")
}

func (d verifiedPackageDownloader) DownloadPackage(context.Context, string) (DownloadedPackage, error) {
	return DownloadedPackage{Path: d.path, CanonicalName: d.name}, nil
}

func TestPublishUsesVerifiedSourceNameAndRetainsProvenance(t *testing.T) {
	for _, document := range []string{
		"---\nname: Persona / Crowd Insight\ndescription: Authorized fixture.\n---\n# Body\n",
		"# Body\n\nAuthorized fixture.\n",
	} {
		db := testutil.NewTestDB(t)
		zipPath := filepath.Join(t.TempDir(), "fixture.zip")
		testutil.WriteSkillZip(t, zipPath, map[string][]byte{"SKILL.md": []byte(document)})
		svc := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir())), Downloader: verifiedPackageDownloader{zipPath, "persona"}})
		const sourceURL = "https://skillhub.cn/skills/owner/persona?version=1.0.0#install"
		published, err := svc.Publish(context.Background(), PublishRequest{AdminUserID: "admin", Source: SourceInput{Type: "url", URL: sourceURL}})
		if err != nil {
			t.Fatal(err)
		}
		var skill skillRow
		if err := db.Where("id = ?", published.SourceSkillID).Take(&skill).Error; err != nil || skill.SkillName != "persona" {
			t.Fatalf("published identity=%q err=%v", skill.SkillName, err)
		}
		var revision skillRevisionRow
		if err := db.Where("id = ?", *skill.HeadRevisionID).Take(&revision).Error; err != nil || revision.SourceRefID != sourceURL {
			t.Fatalf("source provenance=%q err=%v", revision.SourceRefID, err)
		}
	}
}

func TestPublishExternalDocumentCompatibility(t *testing.T) {
	for _, sourceType := range []string{"url", "uploaded_zip"} {
		for _, description := range []string{"Analyze discussions for: useful patterns.", strings.Repeat("仅使用明确授权的材料。", 150)} {
			if sourceType == "uploaded_zip" && utf8.RuneCountInString(description) > 1024 {
				continue // Local packages retain the strict metadata length contract.
			}
			t.Run(sourceType+"/"+description[:12], func(t *testing.T) {
				db := testutil.NewTestDB(t)
				zipPath := filepath.Join(t.TempDir(), "fixture.zip")
				document := "---\nname: fixture\ndescription: " + description + "\ncustom: retained\n---\n# Original body\n"
				testutil.WriteSkillZip(t, zipPath, map[string][]byte{"SKILL.md": []byte(document), "assets/image.png": {0, 255, 137}})
				objects := NewLocalObjectStore(t.TempDir())
				svc := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, objects), Downloader: githubSourceZipDownloader(zipPath)})
				published, err := svc.Publish(context.Background(), PublishRequest{AdminUserID: "admin", Source: SourceInput{Type: sourceType, StoredPath: zipPath, URL: "https://example.com/fixture.zip?download=1#install"}})
				if err != nil {
					t.Fatal(err)
				}
				var skill skillRow
				if err := db.Where("id = ?", published.SourceSkillID).Take(&skill).Error; err != nil {
					t.Fatal(err)
				}
				if skill.SkillName != "fixture" || utf8.RuneCountInString(skill.Description) > skillmetadata.MaxSkillDescriptionLength {
					t.Fatalf("published metadata = %#v", skill)
				}
				var entry skillRevisionEntryRow
				if err := db.Where("revision_id = ? AND path = ?", *skill.HeadRevisionID, "SKILL.md").Take(&entry).Error; err != nil {
					t.Fatal(err)
				}
				var blob skillBlobRow
				if err := db.Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(blob.Content), "# Original body\n") || !strings.Contains(string(blob.Content), "custom: retained") {
					t.Fatalf("source content lost: %s", blob.Content)
				}
				if utf8.RuneCountInString(description) > 1024 && !strings.Contains(string(blob.Content), description) {
					t.Fatal("full original description was lost")
				}
				if count := testutil.CountRows(t, db, "skill_revision_entries", "revision_id = ? AND path = ?", *skill.HeadRevisionID, "assets/image.png"); count != 1 {
					t.Fatal("binary resource was lost")
				}
				entry = skillRevisionEntryRow{}
				if err := db.Where("revision_id = ? AND path = ?", *skill.HeadRevisionID, "assets/image.png").Take(&entry).Error; err != nil {
					t.Fatal(err)
				}
				blob = skillBlobRow{}
				if err := db.Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil || blob.StorageKey == nil {
					t.Fatalf("binary object metadata invalid: %#v, %v", blob, err)
				}
				resource, err := os.ReadFile(filepath.Join(objects.root, filepath.FromSlash(*blob.StorageKey)))
				if err != nil || !bytes.Equal(resource, []byte{0, 255, 137}) {
					t.Fatalf("binary resource=%v err=%v", resource, err)
				}
			})
		}
	}
}
