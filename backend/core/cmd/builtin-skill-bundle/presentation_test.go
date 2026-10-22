package main

import (
	"bytes"
	"context"
	b "lazymind/core/skillv2/builtin"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresentationRebindAndCoverage(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "marketplace"), 0755)
	os.WriteFile(filepath.Join(root, "marketplace/locales.yaml"), []byte("schema_version: 1\nlocales:\n  zh-CN:\n    tags: {tools: 工具}\n    skills:\n      stable: {name: 名称, description: 描述}\n"), 0644)
	sources := []sourceInput{{URL: "https://example.com/a", MarketVisible: true, PresentationID: "stable"}}
	entries := map[string]b.CatalogSkill{"https://example.com/a": {UID: "new-uid", Tags: []string{"tools"}, Category: "tools"}}
	p, err := compilePresentation(filepath.Join(root, "sources.yaml"), sources, entries)
	if err != nil {
		t.Fatal(err)
	}
	if p.Skills["new-uid"].PresentationID != "stable" {
		t.Fatal("not rebound")
	}
	duplicate := append(append([]sourceInput{}, sources...), sources[0])
	if _, err := compilePresentation(filepath.Join(root, "sources.yaml"), duplicate, entries); err == nil {
		t.Fatal("duplicate binding accepted")
	}
	entries["https://example.com/a"] = b.CatalogSkill{UID: "new-uid", Tags: []string{"untranslated"}, Category: "tools"}
	if _, err := compilePresentation(filepath.Join(root, "sources.yaml"), sources, entries); err == nil {
		t.Fatal("missing tag accepted")
	}
	entries["https://example.com/a"] = b.CatalogSkill{UID: "new-uid", Category: "tools"}
	sources[0].PresentationID = "missing"
	if _, err := compilePresentation(filepath.Join(root, "sources.yaml"), sources, entries); err == nil {
		t.Fatal("missing binding accepted")
	}
}

func TestPresentationNormalAndCatalogOnlyPreserveIdentity(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "demo"), 0755)
	os.MkdirAll(filepath.Join(root, "marketplace"), 0755)
	os.WriteFile(filepath.Join(root, "demo/SKILL.md"), []byte("---\nname: demo\ndescription: canonical\n---\n# Original\n"), 0644)
	source := filepath.Join(root, "sources.yaml")
	os.WriteFile(source, []byte("schema_version: 1\nbundled_skills:\n  - uid: demo\n    path: demo\n    category: tools\n    version: 1.0.0\n    presentation_id: stable\nskills: []\n"), 0644)
	localePath := filepath.Join(root, "marketplace/locales.yaml")
	text := "schema_version: 1\nlocales:\n  zh-CN:\n    tags: {tools: 工具}\n    skills:\n      stable: {name: 名称, description: 描述}\n"
	os.WriteFile(localePath, []byte(text), 0644)
	opts := options{Sources: source, Lock: filepath.Join(root, "lock.json"), Cache: filepath.Join(root, "cache"), Output: filepath.Join(root, "normal", "builtin-skills")}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected network %s", r.URL)
		return nil, nil
	})}
	if err := run(context.Background(), opts, client); err != nil {
		t.Fatal(err)
	}
	lock, _ := os.ReadFile(opts.Lock)
	catalog, _ := os.ReadFile(filepath.Join(opts.Output, "catalog.json"))
	original := readCatalog(t, filepath.Join(opts.Output, "catalog.json")).Skills[0]
	archive, _ := os.ReadFile(filepath.Join(opts.Output, original.PackageFile))
	os.WriteFile(localePath, []byte(strings.Replace(text, "名称", "新名称", 1)), 0644)
	opts.Output = filepath.Join(root, "offline", "builtin-skills")
	opts.FrozenLockfile = true
	opts.CatalogOnly = true
	if err := run(context.Background(), opts, client); err != nil {
		t.Fatal(err)
	}
	afterLock, _ := os.ReadFile(opts.Lock)
	afterCatalog, _ := os.ReadFile(filepath.Join(opts.Output, "catalog.json"))
	afterArchive, _ := os.ReadFile(filepath.Join(opts.Output, original.PackageFile))
	if !bytes.Equal(lock, afterLock) || !bytes.Equal(catalog, afterCatalog) || !bytes.Equal(archive, afterArchive) {
		t.Fatal("presentation changed canonical artifact")
	}
	p, err := b.LoadPresentation(filepath.Join(opts.Output, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Skills["demo"].Locales["zh-CN"].Name != "新名称" {
		t.Fatal("offline presentation not regenerated")
	}
}
