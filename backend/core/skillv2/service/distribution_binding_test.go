package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateBuiltinSkillPersistsDistributionBaseline(t *testing.T) {
	db := newSkillV2TestDB(t)
	root := t.TempDir()
	zipPath := filepath.Join(root, "builtin.zip")
	writeSkillZip(t, zipPath, map[string][]byte{
		"SKILL.md": []byte("---\nname: demo\ndescription: demo skill\n---\n# Demo\n"),
		"guide.md": []byte("guide\n"),
	})
	service := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(root)), Clock: fixedClock()})
	response, err := service.CreateSkill(context.Background(), CreateSkillRequest{
		OwnerUserID: "user_001", CreateUserID: "user_001", Name: "demo", Category: "research", Description: "demo skill",
		OriginBuiltinSkillUID: "bsk_demo", Source: SourceInput{Type: "builtin_zip", StoredPath: zipPath, Filename: "bsk_demo@1.0.0#" + strings.Repeat("a", 64)},
		Distribution: &DistributionSource{BuiltinUID: "bsk_demo", Version: "1.0.0", ArchiveSHA256: strings.Repeat("a", 64), TreeSHA256: strings.Repeat("b", 64)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		table string
		where string
		args  []any
	}{
		{table: "skill_distribution_artifacts", where: "archive_sha256 = ?", args: []any{strings.Repeat("a", 64)}},
		{table: "skill_distribution_bindings", where: "skill_id = ?", args: []any{response.SkillID}},
		{table: "skill_distribution_entries", where: "archive_sha256 = ?", args: []any{strings.Repeat("a", 64)}},
		{table: "skill_revision_distributions", where: "revision_id = ?", args: []any{response.HeadRevisionID}},
	} {
		var count int64
		if err := db.Table(check.table).Where(check.where, check.args...).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatalf("%s has no baseline rows", check.table)
		}
	}
}
