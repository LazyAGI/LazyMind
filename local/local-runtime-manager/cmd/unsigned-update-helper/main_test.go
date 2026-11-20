//go:build darwin && lazymind_unsigned_test

package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestIsolatedBundleExtractionRejectsAliasesAndLimitsSize(t *testing.T) {
	type entry struct {
		name, content string
		mode          os.FileMode
	}
	const app = "LazyMind Test.app"
	for _, tc := range []struct {
		name    string
		entries []entry
		limit   int64
		valid   bool
	}{
		{"internal symlink", []entry{
			{app + "/Contents/payload", "data", 0600},
			{app + "/Contents/alias", "payload", os.ModeSymlink | 0777},
		}, 100, true},
		{"outside bundle", []entry{
			{app + "/Contents/alias", "../../../outside", os.ModeSymlink | 0777},
		}, 100, false},
		{"member under symlink", []entry{
			{app + "/Contents/alias", "runtime", os.ModeSymlink | 0777},
			{app + "/Contents/alias/payload", "data", 0600},
		}, 100, false},
		{"case collision", []entry{
			{app + "/Contents/payload", "data", 0600},
			{app + "/Contents/Payload", "other", 0600},
		}, 100, false},
		{"expanded size", []entry{
			{app + "/Contents/payload", "data", 0600},
		}, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			archive := filepath.Join(directory, "package.zip")
			file, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			writer := zip.NewWriter(file)
			for _, member := range tc.entries {
				header := &zip.FileHeader{Name: member.name}
				header.SetMode(member.mode)
				target, err := writer.CreateHeader(header)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := target.Write([]byte(member.content)); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(directory, "unpacked")
			err = extract(t.Context(), archive, destination, tc.limit)
			if !tc.valid {
				if err == nil {
					t.Fatal("invalid package extracted")
				}
				if _, err := os.Stat(destination); !os.IsNotExist(err) {
					t.Fatal("failed validation must not start extraction")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(destination, app, "Contents", "alias"))
			if err != nil || string(raw) != "data" {
				t.Fatalf("ditto did not retain the internal link: %s, %v", raw, err)
			}
		})
	}
}
