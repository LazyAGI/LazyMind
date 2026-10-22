package service

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/skillv2/testutil"
	"os"
	"path/filepath"
	"testing"
)

type installFixtureDownloader struct {
	path  string
	calls int
}

func (d *installFixtureDownloader) Download(context.Context, string) (DownloadedZip, error) {
	d.calls++
	return DownloadedZip{Path: d.path}, nil
}
func TestInstallURLSkillIsDisabledAndRetryDoesNotDownload(t *testing.T) {
	db := testutil.NewTestDB(t)
	archive := filepath.Join(t.TempDir(), "skill.zip")
	testutil.WriteSkillZip(t, archive, map[string][]byte{"SKILL.md": []byte("---\nname: installed\ndescription: Imported skill.\n---\nBody.\n")})
	downloader := &installFixtureDownloader{path: archive}
	service := NewSkillService(SkillServiceDeps{DB: db.DB, Downloader: downloader, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	req := InstallSkillRequest{OwnerUserID: "user", URL: "https://example.test/skill.zip"}
	first, err := service.InstallSkill(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.InstallSkill(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.SkillID != second.SkillID || first.Status != "installed" || second.Status != "already_installed" || first.Enabled || second.Enabled {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if downloader.calls != 1 {
		t.Fatalf("downloads=%d, want 1", downloader.calls)
	}
	var skill testutil.SkillRow
	if err := db.Where("id = ?", first.SkillID).Take(&skill).Error; err != nil {
		t.Fatal(err)
	}
	if skill.IsEnabled || skill.HeadRevisionID == nil {
		t.Fatalf("installed skill=%+v", skill)
	}
	var revision testutil.SkillRevisionRow
	if err := db.Where("id = ?", *skill.HeadRevisionID).Take(&revision).Error; err != nil {
		t.Fatal(err)
	}
	if revision.SourceRefID != req.URL || revision.SourceRefType != "url" {
		t.Fatalf("provenance=%+v", revision)
	}
}
func TestInstallPackageFailurePreservesExistingSkill(t *testing.T) {
	db := testutil.NewTestDB(t)
	service := NewSkillService(SkillServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	archive := filepath.Join(t.TempDir(), "skill.zip")
	testutil.WriteSkillZip(t, archive, map[string][]byte{"SKILL.md": []byte("---\nname: installed\ndescription: Imported skill.\n---\nBody.\n")})
	data, _ := os.ReadFile(archive)
	first, err := service.InstallSkill(context.Background(), InstallSkillRequest{OwnerUserID: "user", PackageZIP: data, ExpectedName: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.InstallSkill(context.Background(), InstallSkillRequest{OwnerUserID: "user", PackageZIP: data, ExpectedName: "different"})
	if err == nil {
		t.Fatal("expected name mismatch")
	}
	var count int64
	db.Table("skills").Where("id = ? AND deleted_at IS NULL", first.SkillID).Count(&count)
	if count != 1 {
		t.Fatalf("existing active skills=%d", count)
	}
}
func TestInstallSkillFailureHasNoPartialPackage(t *testing.T) {
	db := testutil.NewTestDB(t)
	archive := filepath.Join(t.TempDir(), "skill.zip")
	testutil.WriteSkillZip(t, archive, map[string][]byte{"SKILL.md": []byte("---\nname: installed\ndescription: Imported skill.\n---\nBody.\n")})
	service := NewSkillService(SkillServiceDeps{DB: db.DB, Downloader: &installFixtureDownloader{path: archive}, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	db.Callback().Create().Before("gorm:create").Register("fail_install_draft", func(tx *gorm.DB) {
		if tx.Statement.Table == "skill_drafts" {
			tx.AddError(errors.New("forced draft failure"))
		}
	})
	_, err := service.InstallSkill(context.Background(), InstallSkillRequest{OwnerUserID: "user", URL: "https://example.test/skill.zip"})
	if err == nil {
		t.Fatal("expected failure")
	}
	for _, table := range []string{"skills", "skill_revisions", "skill_revision_entries", "skill_drafts", "skill_blobs"} {
		var count int64
		db.Table(table).Count(&count)
		if count != 0 {
			t.Fatalf("%s rows=%d", table, count)
		}
	}
}

type concurrentInstallDownloader struct {
	path     string
	prepared chan struct{}
	release  chan struct{}
}

func (d concurrentInstallDownloader) Download(ctx context.Context, _ string) (DownloadedZip, error) {
	d.prepared <- struct{}{}
	select {
	case <-ctx.Done():
		return DownloadedZip{}, ctx.Err()
	case <-d.release:
		return DownloadedZip{Path: d.path}, nil
	}
}
func TestInstallSkillConcurrentRetriesCommitOnlyOnePackage(t *testing.T) {
	db := testutil.NewTestDB(t)
	archive := filepath.Join(t.TempDir(), "skill.zip")
	testutil.WriteSkillZip(t, archive, map[string][]byte{"SKILL.md": []byte("---\nname: concurrent\ndescription: Concurrent import.\n---\nBody.\n")})
	prepared := make(chan struct{}, 2)
	release := make(chan struct{})
	service := NewSkillService(SkillServiceDeps{DB: db.DB, Downloader: concurrentInstallDownloader{archive, prepared, release}, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	type result struct {
		response InstallSkillResponse
		err      error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			response, err := service.InstallSkill(context.Background(), InstallSkillRequest{OwnerUserID: "user", URL: "https://example.test/same.zip"})
			results <- result{response, err}
		}()
	}
	<-prepared
	<-prepared
	close(release)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("errors: %v / %v", first.err, second.err)
	}
	if first.response.SkillID != second.response.SkillID {
		t.Fatalf("different package IDs: %+v / %+v", first.response, second.response)
	}
	var count int64
	db.Table("skills").Where("deleted_at IS NULL").Count(&count)
	if count != 1 {
		t.Fatalf("active packages=%d", count)
	}
}
