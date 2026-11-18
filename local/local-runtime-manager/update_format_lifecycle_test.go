package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func emptyLifecycleLazyLLM(t *testing.T, f *updateStateFixture) {
	t.Helper()
	if err := f.databases["lazyllm"].Close(); err != nil {
		t.Fatal(err)
	}
	filename := f.cfg.UpdateStorage.Databases["lazyllm"]
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(filename + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	f.databases["lazyllm"] = db
}

func initializeLifecycleProcessor(t *testing.T, f *updateStateFixture) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "update-format", "lazyllm-service.json"))
	var captured struct{ Shape updateDatabaseShape }
	if err != nil || json.Unmarshal(raw, &captured) != nil || len(captured.Shape.Schema) != 40 {
		t.Fatalf("read full service owner fixture: %v", err)
	}
	sort.SliceStable(captured.Shape.Schema, func(i, j int) bool {
		return captured.Shape.Schema[i].Type == "table" && captured.Shape.Schema[j].Type != "table"
	})
	for _, entry := range captured.Shape.Schema {
		var exists int
		if err := f.databases["lazyllm"].QueryRow("SELECT COUNT(*) FROM sqlite_schema WHERE name=?", entry.Name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			if _, err := f.databases["lazyllm"].Exec(entry.SQL); err != nil {
				t.Fatalf("restore processor owner object %s: %v", entry.Name, err)
			}
		}
	}
}

func TestUpdateFormatV2LazyLLMLifecycle(t *testing.T) {
	for _, state := range []string{"lightweight", "processor"} {
		t.Run(state, func(t *testing.T) {
			f := actualUpdateFormatFixture(t, 2)
			profile := f.measure(t).DataProfile // Existing DocManager-only installations.
			ragDisabled := state == "lightweight"
			if ragDisabled {
				emptyLifecycleLazyLLM(t, f)
			} else {
				setActualFixtureRAG(t, f, strings.Repeat("a", 64))
				initializeLifecycleProcessor(t, f)
			}
			got := f.measure(t)
			if got.DataProfile != profile {
				t.Fatalf("normal LazyLLM initialization changed format: %s -> %s", profile, got.DataProfile)
			}
			target, dirty, err := inspectStoredUpdateFormat(context.Background(), f.cfg.UpdateStorage, ragDisabled)
			if err != nil || dirty || target != profile {
				t.Fatalf("target startup rejected original storage: format=%s dirty=%v err=%v", target, dirty, err)
			}
		})
	}
}

func TestUpdateFormatV2LazyLLMLifecycleRejectsUnknownShapes(t *testing.T) {
	for _, scenario := range []string{"extra-table", "changed-processor-column", "missing-processor-table", "empty-nonzero-version", "missing-file"} {
		t.Run(scenario, func(t *testing.T) {
			f := actualUpdateFormatFixture(t, 2)
			initializeLifecycleProcessor(t, f)
			var statement string
			switch scenario {
			case "extra-table":
				statement = "CREATE TABLE unexpected_table(id TEXT)"
			case "changed-processor-column":
				statement = "ALTER TABLE lazyllm_algorithm ADD COLUMN unexpected TEXT"
			case "missing-processor-table":
				statement = "DROP TABLE lazyllm_waiting_task_queue"
			case "empty-nonzero-version":
				emptyLifecycleLazyLLM(t, f)
				statement = "PRAGMA user_version=1"
			case "missing-file":
				if err := f.databases["lazyllm"].Close(); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(f.cfg.UpdateStorage.Databases["lazyllm"]); err != nil {
					t.Fatal(err)
				}
			}
			if statement != "" {
				if _, err := f.databases["lazyllm"].Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			got, err := measureDesktopUpdateState(context.Background(), f.cfg, f.paths)
			if err == nil || got.DataProfile != "" {
				t.Fatalf("unknown/damaged LazyLLM authorized an update: %+v err=%v", got, err)
			}
			if target, _, err := inspectStoredUpdateFormat(context.Background(), f.cfg.UpdateStorage, true); err == nil || target != "" {
				t.Fatalf("target accepted unknown/damaged LazyLLM: %s err=%v", target, err)
			}
		})
	}
}
