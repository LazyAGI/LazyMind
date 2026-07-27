package currentmemory

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

const currentMemoryPostgresTestDSNEnv = "CURRENT_MEMORY_POSTGRES_TEST_DSN"

func TestCurrentMemoryPostgresContract(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv(currentMemoryPostgresTestDSNEnv))
	if dsn == "" {
		t.Skip(currentMemoryPostgresTestDSNEnv + " is not configured")
	}
	db, err := orm.Connect(orm.DriverPostgres, dsn)
	if err != nil {
		t.Fatalf("connect PostgreSQL: %v", err)
	}
	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatalf("get PostgreSQL sql.DB: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	var databaseName string
	if err := db.Raw("SELECT current_database()").Scan(&databaseName).Error; err != nil {
		t.Fatalf("read PostgreSQL database name: %v", err)
	}
	if !strings.HasPrefix(databaseName, "current_memory_test_") {
		t.Fatalf(
			"refusing destructive integration setup in database %q; expected current_memory_test_*",
			databaseName,
		)
	}
	upMigration, downMigration := readCurrentMemoryPostgresMigrations(t)
	if err := db.Exec(downMigration).Error; err != nil {
		t.Fatalf("clean PostgreSQL Current Memory schema: %v", err)
	}
	if err := db.Exec(upMigration).Error; err != nil {
		t.Fatalf("apply PostgreSQL Current Memory migration: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Exec(downMigration).Error; err != nil {
			t.Errorf("clean PostgreSQL Current Memory schema: %v", err)
		}
	})

	module := NewModule(db.DB)
	repository := NewRepository(db.DB)
	profile, err := module.GetProfile(t.Context(), "postgres-user")
	if err != nil {
		t.Fatalf("initialize and read Profile: %v", err)
	}
	if memoryList(t, profile.Document, "identity.aliases") == nil {
		t.Fatal("default Profile aliases must be an array")
	}
	profile, err = module.PatchProfile(
		t.Context(),
		"postgres-user",
		CurrentMemoryOperationsRequest{Operations: []CurrentMemoryOperation{
			{Op: "clear", Path: "identity.preferred_name"},
			{Op: "clear", Path: "identity.aliases"},
		}},
	)
	if err != nil {
		t.Fatalf("patch Profile null and empty array: %v", err)
	}
	if memoryString(t, profile.Document, "identity.preferred_name") != "" ||
		len(memoryList(t, profile.Document, "identity.aliases")) != 0 {
		t.Fatalf("Profile tri-state result = %#v", profile.Document)
	}

	remoteProfile := strings.Replace(
		DefaultProfileYAML,
		"residence: null",
		"residence: Europe/London",
		1,
	)
	if err := repository.UpdateFileContent(
		t.Context(),
		"postgres-user",
		ProfilePath,
		[]byte(remoteProfile),
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("simulate RemoteFS Profile write: %v", err)
	}
	profile, err = module.GetProfile(t.Context(), "postgres-user")
	if err != nil {
		t.Fatalf("public Module read after repository write: %v", err)
	}
	if memoryString(t, profile.Document, "locale.residence") != "Europe/London" {
		t.Fatalf("Module did not observe repository write: %#v", profile.Document)
	}
	soulName := "PostgreSQL Soul"
	if _, err := module.PatchSoul(
		t.Context(),
		"postgres-user",
		CurrentMemoryOperationsRequest{Operations: []CurrentMemoryOperation{
			{Op: "set", Path: "identity.name", Value: &soulName},
		}},
	); err != nil {
		t.Fatalf("public Soul patch: %v", err)
	}
	soulEntry, err := repository.GetEntry(
		t.Context(),
		"postgres-user",
		SoulPath,
	)
	if err != nil {
		t.Fatalf("repository read after public Soul patch: %v", err)
	}
	soul, err := ParseSoul(soulEntry.Content)
	if err != nil || memoryString(t, soul, "identity.name") != "PostgreSQL Soul" {
		t.Fatalf("repository did not observe public Soul patch: %#v err=%v", soul, err)
	}

	const timestamp = "2026-07-20T09:30:00+08:00"
	preferenceContent, err := RenderPreferences(PreferenceDocument{Preferences: []PreferenceItem{
		{
			Name:      "pref.first",
			Summary:   "First",
			Ref:       "references/first.md",
			CreatedAt: timestamp,
			UpdatedAt: timestamp,
		},
		{
			Name:      "pref.second",
			Summary:   "Second",
			Ref:       "references/second.md",
			CreatedAt: timestamp,
			UpdatedAt: timestamp,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateFileContent(
		t.Context(),
		"postgres-user",
		PreferencePath,
		preferenceContent,
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("seed Preference index: %v", err)
	}
	listed, err := module.ListPreferences(t.Context(), "postgres-user")
	if err != nil {
		t.Fatalf("list Preferences: %v", err)
	}
	reordered, err := module.ReorderPreferences(
		t.Context(),
		"postgres-user",
		CurrentMemoryPreferenceOrderRequest{
			OrderedNames: []string{"pref.second", "pref.first"},
			ExpectedETag: listed.ETag,
		},
	)
	if err != nil {
		t.Fatalf("reorder Preferences: %v", err)
	}
	if reordered.Items[0].Name != "pref.second" ||
		reordered.ETag == listed.ETag {
		t.Fatalf("unexpected reordered Preferences: %#v", reordered)
	}
	_, err = module.ReorderPreferences(
		t.Context(),
		"postgres-user",
		CurrentMemoryPreferenceOrderRequest{
			OrderedNames: []string{"pref.first", "pref.second"},
			ExpectedETag: listed.ETag,
		},
	)
	var conflict *ETagConflictError
	if !errors.As(err, &conflict) || conflict.CurrentETag != reordered.ETag {
		t.Fatalf("stale etag error = %#v", err)
	}
	if err := module.DeletePreference(
		t.Context(),
		"postgres-user",
		"pref.first",
	); err != nil {
		t.Fatalf("delete Preference: %v", err)
	}
	if err := module.DeletePreference(
		t.Context(),
		"postgres-user",
		"pref.first",
	); err != nil {
		t.Fatalf("idempotent delete Preference: %v", err)
	}
	remaining, err := module.ListPreferences(t.Context(), "postgres-user")
	if err != nil {
		t.Fatal(err)
	}
	if remaining.TotalSize != 1 || remaining.Items[0].Name != "pref.second" {
		t.Fatalf("remaining Preferences = %#v", remaining)
	}
}

func readCurrentMemoryPostgresMigrations(t *testing.T) (string, string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Current Memory PostgreSQL integration test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(file), "..", "migrations")
	read := func(suffix string) string {
		t.Helper()
		matches, err := filepath.Glob(filepath.Join(migrationsDir, "*_"+suffix))
		if err != nil {
			t.Fatalf("find %s migration: %v", suffix, err)
		}
		if len(matches) != 1 {
			t.Fatalf("find %s migration: got %d matches, want 1", suffix, len(matches))
		}
		body, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatalf("read %s: %v", matches[0], err)
		}
		return string(body)
	}
	return read(
			"create_memory_current_entries.up.sql",
		), read(
			"create_memory_current_entries.down.sql",
		)
}
