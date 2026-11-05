package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func actualUpdateFormatFixture(t *testing.T, version int) *updateStateFixture {
	t.Helper()
	f := newUpdateStateFixture(t)
	for family, db := range f.databases {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		filename := f.cfg.UpdateStorage.Databases[family]
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(filename + suffix); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		var captured struct {
			Producer string
			Shape    updateDatabaseShape
		}
		raw, err := os.ReadFile(filepath.Join("testdata", "update-format", family+".json"))
		if err != nil || json.Unmarshal(raw, &captured) != nil || captured.Producer == "" {
			t.Fatalf("read actual %s producer fixture: %v", family, err)
		}
		database, err := sql.Open("sqlite", filename)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.Close() })
		database.SetMaxOpenConns(1)
		if err := database.Ping(); err != nil {
			t.Fatal(err)
		}
		rank := map[string]int{"table": 0, "index": 1, "view": 2, "trigger": 3}
		sort.SliceStable(captured.Shape.Schema, func(i, j int) bool {
			a, b := captured.Shape.Schema[i], captured.Shape.Schema[j]
			if rank[a.Type] != rank[b.Type] {
				return rank[a.Type] < rank[b.Type]
			}
			return a.Name < b.Name
		})
		for _, entry := range captured.Shape.Schema {
			var exists int
			if err := database.QueryRow("SELECT COUNT(*) FROM sqlite_schema WHERE name = ?", entry.Name).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			// FTS creates its shadow tables; never create a second copy.
			if exists != 0 {
				continue
			}
			if _, err := database.Exec(entry.SQL); err != nil {
				t.Fatalf("restore %s object %s: %v", family, entry.Name, err)
			}
		}
		if family == "core" {
			if _, err := database.Exec("INSERT INTO schema_migrations(version,dirty) VALUES (?,0)", captured.Shape.CoreVersion); err != nil {
				t.Fatal(err)
			}
			for _, entry := range captured.Shape.CoreHistory {
				if _, err := database.Exec("INSERT INTO schema_migration_history(version,name) VALUES (?,?)", entry.Version, entry.Name); err != nil {
					t.Fatal(err)
				}
			}
		}
		f.databases[family] = database
	}
	st, err := readRuntimeState(f.paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	// Version 1 retains the legacy exact-instance measurement contract.
	// Version 2 introduces the stable-format resolved-storage contract.
	f.cfg.UpdateStorage.Version = version
	st.Config.UpdateStorage.Version = version
	if err := writeRuntimeState(f.paths.StateFile, st); err != nil {
		t.Fatal(err)
	}
	return f
}

func mutateActualSegmentStore(t *testing.T, f *updateStateFixture, action string) {
	t.Helper()
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "-I", "-B", filepath.Join("testdata", "update-format", "producer.py"),
		repo, "segments", f.cfg.UpdateStorage.Databases["segments"], action)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("real pinned SQLiteStore %s: %v\n%s", action, err, output)
	}
}

func setActualFixtureRAG(t *testing.T, f *updateStateFixture, revision string) {
	t.Helper()
	entry := pythonComponent{
		ID: "rag", Revision: revision, SHA256: strings.Repeat("b", 64),
		BaseFingerprint: strings.Repeat("c", 64), Platform: runtime.GOOS,
		Arch: runtime.GOARCH, PythonABI: "cp311", Packages: map[string]string{"fixture-package": "1"},
	}
	write := func(filename string, value any) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(f.paths.ResourcesRoot, "config", "python-components.json"),
		pythonComponentCatalog{SchemaVersion: 1, Platform: entry.Platform, Arch: entry.Arch, Components: map[string]pythonComponent{"rag": entry}})
	dir := filepath.Join(f.paths.RuntimeRoot, "deps", "python-components", "rag", revision)
	if err := os.MkdirAll(filepath.Join(dir, "site-packages"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(dir, "bundle-manifest.json"), entry)
	if err := os.WriteFile(filepath.Join(dir, ".ready"), []byte(entry.SHA256), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := readRuntimeState(f.paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	st.Config.Algorithm.RAGDisabled = false
	if err := writeRuntimeState(f.paths.StateFile, st); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateFormatV2ActualCollectionsDoNotChangeFormat(t *testing.T) {
	f := actualUpdateFormatFixture(t, 2)
	setActualFixtureRAG(t, f, strings.Repeat("a", 64))
	before := f.measure(t).DataProfile
	for _, action := range []string{
		"write:col_finechunk", "write:col_coarsechunk",
		"drop:col_finechunk", "drop:col_coarsechunk",
	} {
		mutateActualSegmentStore(t, f, action)
		if got := f.measure(t).DataProfile; got != before {
			t.Fatalf("normal operation %s changed format: %s -> %s", action, before, got)
		}
	}
}

func TestUpdateFormatV2OptionalRAGIsNotPartOfDataFormat(t *testing.T) {
	f := actualUpdateFormatFixture(t, 2)
	before := f.measure(t).DataProfile
	setActualFixtureRAG(t, f, strings.Repeat("a", 64))
	if got := f.measure(t); got.DataProfile != before || got.Components["rag"] != strings.Repeat("a", 64) {
		t.Fatalf("RAG must be measured separately from format: %+v", got)
	}
}

func TestUpdateFormatV2RowsAndLegalOptionalAbsenceDoNotChangeFormat(t *testing.T) {
	f := actualUpdateFormatFixture(t, 2)
	before := f.measure(t).DataProfile
	if _, err := f.databases["core-state"].Exec("INSERT INTO state_kv(key,value,expires_at) VALUES ('fixture','synthetic',0)"); err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"scan", "scan-state"} {
		if err := f.databases[family].Close(); err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(f.cfg.UpdateStorage.Databases[family] + suffix); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
	}
	if got := f.measure(t).DataProfile; got != before {
		t.Fatalf("row/optional-presence changed format: %s -> %s", before, got)
	}
}

func TestUpdateFormatV2RejectsUnknownStructureAndBrokenDynamicObjects(t *testing.T) {
	for _, scenario := range []string{"extra-fixed-column", "extra-table", "broken-group", "orphan-fts", "dirty", "history-name"} {
		t.Run(scenario, func(t *testing.T) {
			f := actualUpdateFormatFixture(t, 2)
			_ = f.measure(t)
			var db *sql.DB
			var statement string
			switch scenario {
			case "extra-fixed-column":
				db, statement = f.databases["core-state"], "ALTER TABLE state_kv ADD COLUMN unexpected TEXT"
			case "extra-table":
				db, statement = f.databases["auth"], "CREATE TABLE unexpected_table(id TEXT)"
			case "broken-group":
				mutateActualSegmentStore(t, f, "write:col_finechunk")
				db, statement = f.databases["segments"], "ALTER TABLE col_finechunk ADD COLUMN unexpected TEXT"
			case "orphan-fts":
				db, statement = f.databases["segments"], "CREATE VIRTUAL TABLE orphan_fts USING fts5(content)"
			case "dirty":
				db, statement = f.databases["core"], "UPDATE schema_migrations SET dirty=1"
			case "history-name":
				db, statement = f.databases["core"], "UPDATE schema_migration_history SET name='unknown-history' WHERE version=(SELECT MIN(version) FROM schema_migration_history)"
			}
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			got, err := measureDesktopUpdateState(context.Background(), f.cfg, f.paths)
			if err == nil && !got.Dirty {
				t.Fatalf("invalid structure accepted: %+v", got)
			}
			if err != nil && got.DataProfile != "" {
				t.Fatal("failed inspection returned an authorizing format")
			}
		})
	}
}

func TestLegacyActualProducerShowsWhyV2IsRequired(t *testing.T) {
	f := actualUpdateFormatFixture(t, 1)
	setActualFixtureRAG(t, f, strings.Repeat("a", 64))
	before := f.measure(t).DataProfile
	mutateActualSegmentStore(t, f, "write:col_finechunk")
	if after := f.measure(t).DataProfile; before == after {
		t.Fatal("legacy control no longer represents the frozen exact instance hash")
	}
	// This is a provenance/control check; v2 must keep this operation stable.
}
