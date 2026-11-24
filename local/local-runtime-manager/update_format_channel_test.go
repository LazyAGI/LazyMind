package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestUpdateFormatChannelMigratedFrom758(t *testing.T) {
	// SQLiteGatewayStore.initialize at 38ef21dca, followed by the current
	// initializer, differs from a fresh channel database only in this table.
	dir := filepath.Join("testdata", "update-format")
	var captured struct{ Shape updateDatabaseShape }
	raw, err := os.ReadFile(filepath.Join(dir, "channel.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &captured); err != nil {
		t.Fatal(err)
	}
	legacy, err := os.ReadFile(filepath.Join(dir, "channel-accounts-after-758.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var recipe localFormatRecipe
	if err := json.Unmarshal(updateFormatRecipe, &recipe); err != nil {
		t.Fatal(err)
	}
	shapes := map[string]updateDatabaseShape{}
	for family := range recipe.Families {
		shapes[family] = updateDatabaseShape{}
	}
	shapes["channel"] = captured.Shape
	// This profile was emitted by the release packager at ecb77cdf6.
	const published = "format-v2-e5f1ba46c439f67bda43648758f5e860cb9df417a634f789da7f7176c71fa5fc"
	fresh, err := stableUpdateFormat(shapes)
	if err != nil || fresh != published {
		t.Fatalf("fresh layout = (%q, %v), want published profile %q", fresh, err, published)
	}
	for i, entry := range captured.Shape.Schema {
		if entry.Name != "channel_accounts" || entry.Type != "table" {
			continue
		}
		migrated := strings.TrimSuffix(string(legacy), "\n")
		if entry.SQL == migrated {
			t.Fatal("expected distinct fresh and migrated SQLite definitions")
		}
		captured.Shape.Schema[i].SQL = migrated
		got, err := stableUpdateFormat(shapes)
		if err != nil || got != fresh {
			t.Errorf("recognized migrated layout = (%q, %v), want %q", got, err, fresh)
		}
		captured.Shape.Schema[i].SQL = strings.Replace(migrated, "label_custom BOOLEAN NOT NULL", "label_custom BOOLEAN", 1)
		if _, err := stableUpdateFormat(shapes); err == nil {
			t.Fatal("accepted a migrated layout with its NOT NULL constraint removed")
		}
		captured.Shape.Schema[i].SQL = migrated
		index := slices.IndexFunc(captured.Shape.Schema, func(e updateSchemaEntry) bool {
			return e.Type == "index" && e.Name == "channel_inbox_claim_idx"
		})
		if index < 0 {
			t.Fatal("captured channel_inbox_claim_idx missing")
		}
		captured.Shape.Schema = slices.Delete(captured.Shape.Schema, index, index+1)
		shapes["channel"] = captured.Shape
		if _, err := stableUpdateFormat(shapes); err == nil {
			t.Fatal("accepted a migrated layout with a missing inbox index")
		}
		return
	}
	t.Fatal("captured channel_accounts table missing")
}
