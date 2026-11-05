package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

//go:embed update-format-v2.json
var updateFormatRecipe []byte

type localFormatRecipe struct {
	Version         int                 `json:"version"`
	Families        map[string]string   `json:"families"`
	Segments        []updateSchemaEntry `json:"segments"`
	LazyLLMVariants []string            `json:"lazyllmVariants"`
}

func shapeDigest(shape updateDatabaseShape, core bool) string {
	hash := sha256.New()
	field := func(value string) { _, _ = fmt.Fprintf(hash, "%d:", len(value)); _, _ = io.WriteString(hash, value) }
	field(strconv.FormatInt(shape.ApplicationID, 10))
	field(strconv.FormatInt(shape.UserVersion, 10))
	for _, e := range shape.Schema {
		field(e.Type)
		field(e.Name)
		field(e.Table)
		field(e.SQL)
	}
	if core {
		field(strconv.FormatUint(shape.CoreVersion, 10))
		for _, e := range shape.CoreHistory {
			field(strconv.FormatUint(e.Version, 10))
			field(e.Name)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

var collectionIdentifier = regexp.MustCompile(`^col_[A-Za-z0-9_]+$`)

func recognizeSegments(shape updateDatabaseShape, template []updateSchemaEntry) bool {
	if shape.ApplicationID != 0 || shape.UserVersion != 0 {
		return false
	}
	objects := map[string]updateSchemaEntry{}
	for _, e := range shape.Schema {
		objects[e.Type+"\x00"+e.Name] = e
	}
	var base updateSchemaEntry
	for _, e := range template {
		if e.Name == "$group" && e.Type == "table" {
			base = e
		}
	}
	if base.SQL == "" {
		return false
	}
	groups := []string{}
	for _, e := range shape.Schema {
		if e.Type == "table" && collectionIdentifier.MatchString(e.Name) && e.SQL == strings.ReplaceAll(base.SQL, "$group", e.Name) {
			groups = append(groups, e.Name)
		}
	}
	for _, group := range groups {
		for _, e := range template {
			expected := updateSchemaEntry{Type: e.Type, Name: strings.ReplaceAll(e.Name, "$group", group), Table: strings.ReplaceAll(e.Table, "$group", group), SQL: strings.ReplaceAll(e.SQL, "$group", group)}
			key := expected.Type + "\x00" + expected.Name
			if got, ok := objects[key]; !ok || got != expected {
				return false
			}
			delete(objects, key)
		}
	}
	return len(objects) == 0
}

func stableUpdateFormat(shapes map[string]updateDatabaseShape) (string, error) {
	var recipe localFormatRecipe
	if json.Unmarshal(updateFormatRecipe, &recipe) != nil || recipe.Version != 2 || len(recipe.Families) != 9 {
		return "", updateStateFailure("data_unknown")
	}
	for family, want := range recipe.Families {
		shape, ok := shapes[family]
		if !ok {
			return "", updateStateFailure("data_unknown")
		}
		if !shape.Present {
			continue
		} // Only owner-approved optional absence reaches here.
		if family == "segments" {
			if !recognizeSegments(shape, recipe.Segments) {
				return "", updateStateFailure("data_unknown")
			}
		} else {
			got := shapeDigest(shape, family == "core")
			if got != want && (family != "lazyllm" || !slices.Contains(recipe.LazyLLMVariants, got)) {
				return "", updateStateFailure("data_unknown")
			}
		}
	}
	normalized, _ := json.Marshal(recipe)
	hash := sha256.Sum256(normalized)
	return "format-v2-" + hex.EncodeToString(hash[:]), nil
}

// This same read-only boundary is used by target startup with the frozen source
// paths. It never reconstructs paths, creates a database or runs migrations.
func inspectStoredUpdateFormat(ctx context.Context, storage *UpdateStorageSnapshot, ragDisabled bool) (string, bool, error) {
	if storage == nil || storage.Version != 2 || len(storage.Databases) != 9 || storage.SegmentStore != "SQLiteStore" {
		return "", false, updateStateFailure("unsupported_storage")
	}
	shapes := map[string]updateDatabaseShape{}
	seen := map[string]bool{}
	families := make([]string, 0, len(storage.Databases))
	for f := range storage.Databases {
		families = append(families, f)
	}
	sort.Strings(families)
	for _, family := range families {
		shape, dirty, err := inspectBoundUpdateDatabase(ctx, storage.Databases[family], family, ragDisabled, seen)
		if err != nil || dirty {
			return "", dirty, err
		}
		shapes[family] = shape
	}
	profile, err := stableUpdateFormat(shapes)
	return profile, false, err
}

func measuredComponentMetadata(paths RuntimePaths, components map[string]string) (map[string]desktopupdate.ComponentDescriptor, error) {
	result := map[string]desktopupdate.ComponentDescriptor{}
	if len(components) == 0 {
		return result, nil
	}
	catalog, err := loadPythonComponentCatalogForUpdate(paths)
	if err != nil || catalog == nil {
		return nil, updateStateFailure("component_state_unknown")
	}
	for id, revision := range components {
		e := catalog.Components[id]
		if e.Revision != revision {
			return nil, updateStateFailure("component_state_unknown")
		}
		result[id] = desktopupdate.ComponentDescriptor{ID: id, Revision: e.Revision, SHA256: e.SHA256, Tag: e.Tag, PythonABI: e.PythonABI, BaseFingerprint: e.BaseFingerprint, Packages: e.Packages, Platform: e.Platform, Arch: e.Arch}
	}
	return result, nil
}
