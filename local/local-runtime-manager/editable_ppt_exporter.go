package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Materialize exporter code outside the signed app before attaching mutable
// dependencies. Content-addressed copies remain valid across app upgrades.
func stageEditablePPTExporter(source, deps string) (string, error) {
	if _, err := os.Stat(source); os.IsNotExist(err) {
		return source, nil
	}
	files := make(map[string][]byte)
	digest := sha256.New()
	err := filepath.WalkDir(source, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Name() == "node_modules" || entry.Name() == "tests" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported exporter entry: %s", name)
		}
		rel, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		files[rel] = data
		fmt.Fprintf(digest, "%d:%s:%d:", len(rel), rel, len(data))
		digest.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	id := fmt.Sprintf("%x", digest.Sum(nil))
	root := filepath.Join(deps, "export-runtime")
	target := filepath.Join(root, id)
	ready := func() bool {
		marker, err := os.ReadFile(filepath.Join(target, ".complete"))
		return err == nil && string(marker) == id
	}
	if ready() {
		return target, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(root, ".stage-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	for rel, data := range files {
		name := filepath.Join(staging, rel)
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(name, data, 0o644); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(staging, ".complete"), []byte(id), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(staging, target); err != nil && !ready() {
		return "", err
	}
	return target, nil
}
