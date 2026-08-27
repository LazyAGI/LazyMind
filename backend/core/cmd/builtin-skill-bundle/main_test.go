package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/core/showcase"
	skillbuiltin "lazymind/core/skillv2/builtin"
	skillmetadata "lazymind/core/skillv2/metadata"
	skillpackage "lazymind/core/skillv2/skillpackage"
	skillpatch "lazymind/core/skillv2/skillpatch"
)

func TestResolveSourceMapsNamespacedSkillHubPageToDownloadAPI(t *testing.T) {
	spec, err := resolveSource("https://skillhub.cn/skills/user_7c4df347/gaokao-volunteer-advisor")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Identity != "skillhub:@user_7c4df347/gaokao-volunteer-advisor" {
		t.Fatalf("identity = %q", spec.Identity)
	}
	if spec.ResolvedURL != "https://api.skillhub.cn/api/v1/download?slug=%40user_7c4df347%2Fgaokao-volunteer-advisor" {
		t.Fatalf("resolved URL = %q", spec.ResolvedURL)
	}
}

func TestFrozenDownloadURLPinsSkillHubVersion(t *testing.T) {
	spec, err := resolveSource("https://skillhub.cn/skills/user_5b28ea14/smart-charts")
	if err != nil {
		t.Fatal(err)
	}
	got, err := frozenDownloadURL(spec, skillbuiltin.CatalogSkill{Version: "6.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://api.skillhub.cn/api/v1/download?slug=%40user_5b28ea14%2Fsmart-charts&version=6.2.1"
	if got != want {
		t.Fatalf("frozen download URL = %q, want %q", got, want)
	}
}

func TestFrozenSkillHubBuildPinsAndValidatesVersionWithoutArchiveHash(t *testing.T) {
	archive := makeSkillZipWithVersion(t, "1.2.3")
	requestedURL := ""
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestedURL = request.URL.String()
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), ContentLength: int64(len(archive)), Header: make(http.Header)}, nil
	})}
	spec, err := resolveSource("https://skillhub.cn/skills/demo")
	if err != nil {
		t.Fatal(err)
	}
	locked := skillbuiltin.CatalogSkill{Version: "1.2.3", ArchiveSHA256: strings.Repeat("0", 64), ArchiveSize: 1}
	root := t.TempDir()
	if _, _, _, err := materializeFrozen(context.Background(), client, spec, locked, root, skillpatch.Catalog{}, false); err != nil {
		t.Fatalf("version-pinned build rejected changed archive bytes: %v", err)
	}
	if !strings.Contains(requestedURL, "version=1.2.3") {
		t.Fatalf("request URL was not version-pinned: %q", requestedURL)
	}

	wrongArchive := makeSkillZipWithVersion(t, "2.0.0")
	wrongClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(wrongArchive)), ContentLength: int64(len(wrongArchive)), Header: make(http.Header)}, nil
	})}
	if _, _, _, err := materializeFrozen(context.Background(), wrongClient, spec, locked, root, skillpatch.Catalog{}, false); err == nil || !strings.Contains(err.Error(), "want locked version") {
		t.Fatalf("wrong downloaded version error = %v", err)
	}
}

func TestRunBuildsCatalogAndFrozenModeUsesVerifiedCache(t *testing.T) {
	archive := makeSkillZip(t)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(bytes.NewReader(archive)),
			ContentLength: int64(len(archive)),
			Header:        make(http.Header),
		}, nil
	})}
	root := t.TempDir()
	sources := filepath.Join(root, "sources.yaml")
	lock := filepath.Join(root, "lock.json")
	cache := filepath.Join(root, "cache")
	output := filepath.Join(root, "runtime", "builtin-skills")
	if err := os.WriteFile(sources, []byte("schema_version: 1\nskills:\n  - https://example.test/demo.zip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := options{Sources: sources, Lock: lock, Cache: cache, Output: output}
	if err := run(context.Background(), opts, client); err != nil {
		t.Fatal(err)
	}
	catalog := readCatalog(t, filepath.Join(output, "catalog.json"))
	if len(catalog.Skills) != 1 || catalog.Skills[0].Name != "demo" || catalog.Skills[0].Version != "1.2.3" {
		t.Fatalf("catalog = %#v", catalog)
	}
	if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(catalog.Skills[0].PackageFile))); err != nil {
		t.Fatal(err)
	}
	secondOutput := filepath.Join(root, "runtime-frozen", "builtin-skills")
	opts.Output = secondOutput
	opts.FrozenLockfile = true
	if err := run(context.Background(), opts, http.DefaultClient); err != nil {
		t.Fatalf("frozen cached build failed: %v", err)
	}
}

func TestRunPackagesBundledSkillsIntoTheCatalog(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "research", "local-demo")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: local-demo\ndescription: bundled skill\n---\n# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	referencePath := filepath.Join(skillDir, "references", "guide.md")
	if err := os.WriteFile(referencePath, []byte("guide\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sources := filepath.Join(root, "sources.yaml")
	if err := os.WriteFile(sources, []byte(`schema_version: 1
bundled_skills:
  - uid: bsk_local_demo
    path: research/local-demo
    category: research
    version: 1.0.0
    provider: WorkBuddy
skills: []
`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := options{
		Sources: sources,
		Lock:    filepath.Join(root, "lock.json"),
		Cache:   filepath.Join(root, "cache"),
		Output:  filepath.Join(root, "runtime", "builtin-skills"),
	}
	if err := run(context.Background(), opts, http.DefaultClient); err != nil {
		t.Fatal(err)
	}
	catalog := readCatalog(t, filepath.Join(opts.Output, "catalog.json"))
	if len(catalog.Skills) != 1 || catalog.Skills[0].UID != "bsk_local_demo" || catalog.Skills[0].SourceURL != "builtin://research/local-demo" || catalog.Skills[0].Provider != "WorkBuddy" || !skillbuiltin.CatalogSkillMarketVisible(catalog.Skills[0]) {
		t.Fatalf("catalog = %#v", catalog)
	}
	opts.Output = filepath.Join(root, "runtime-frozen", "builtin-skills")
	opts.FrozenLockfile = true
	if err := run(context.Background(), opts, http.DefaultClient); err != nil {
		t.Fatalf("frozen bundled build failed: %v", err)
	}
	if err := os.WriteFile(referencePath, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts.Output = filepath.Join(root, "runtime-changed", "builtin-skills")
	if err := run(context.Background(), opts, http.DefaultClient); err != nil {
		t.Fatalf("frozen build rejected current bundled Skill source: %v", err)
	}
	changed := readCatalog(t, filepath.Join(opts.Output, "catalog.json")).Skills[0]
	if changed.TreeSHA256 == catalog.Skills[0].TreeSHA256 {
		t.Fatal("changed bundled Skill did not produce a new runtime tree hash")
	}
}

func TestRunAcceptsRemoteSourceMappingWithCategoryAndProvider(t *testing.T) {
	archive := makeSkillZip(t)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), ContentLength: int64(len(archive)), Header: make(http.Header)}, nil
	})}
	root := t.TempDir()
	sources := filepath.Join(root, "sources.yaml")
	if err := os.WriteFile(sources, []byte("schema_version: 1\nskills:\n  - source_url: https://example.test/demo.zip\n    category: search\n    provider: SkillHub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := options{Sources: sources, Lock: filepath.Join(root, "lock.json"), Cache: filepath.Join(root, "cache"), Output: filepath.Join(root, "runtime", "builtin-skills")}
	if err := run(context.Background(), opts, client); err != nil {
		t.Fatal(err)
	}
	entry := readCatalog(t, filepath.Join(opts.Output, "catalog.json")).Skills[0]
	if entry.Category != "search" || entry.Provider != "SkillHub" {
		t.Fatalf("category/provider = %q/%q", entry.Category, entry.Provider)
	}

	opts.Output = filepath.Join(root, "runtime-frozen", "builtin-skills")
	opts.FrozenLockfile = true
	if err := run(context.Background(), opts, http.DefaultClient); err != nil {
		t.Fatalf("frozen provider build failed: %v", err)
	}
}

func TestRunAppliesPatchToDownloadedSkillAndFreezesProvenance(t *testing.T) {
	files := testSkillFiles()
	archive := makeSkillZipFromFiles(t, files)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(bytes.NewReader(archive)),
			ContentLength: int64(len(archive)),
			Header:        make(http.Header),
		}, nil
	})}
	root := t.TempDir()
	sourceURL := "https://example.test/demo.zip"
	spec, err := resolveSource(sourceURL)
	if err != nil {
		t.Fatal(err)
	}
	writeSinglePatch(t, root, resolvedSkillUID(spec), "1.2.3", files, "script.py", "print('patched')\n")
	sources := filepath.Join(root, "sources.yaml")
	if err := os.WriteFile(sources, []byte("schema_version: 1\npatch_catalog: patches/catalog.yaml\nskills:\n  - "+sourceURL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := options{
		Sources: sources,
		Lock:    filepath.Join(root, "lock.json"),
		Cache:   filepath.Join(root, "cache"),
		Output:  filepath.Join(root, "runtime", "builtin-skills"),
	}
	if err := run(context.Background(), opts, client); err != nil {
		t.Fatal(err)
	}
	catalog := readCatalog(t, filepath.Join(opts.Output, "catalog.json"))
	if len(catalog.Skills) != 1 {
		t.Fatalf("catalog = %#v", catalog)
	}
	entry := catalog.Skills[0]
	originHash := sha256.Sum256(archive)
	if entry.OriginArchiveSHA256 != hex.EncodeToString(originHash[:]) || entry.OriginTreeSHA256 != skillpackage.TreeHash(files) || len(entry.AppliedPatches) != 1 || entry.PatchSetSHA256 == "" {
		t.Fatalf("patch provenance = %#v", entry)
	}
	if entry.ArchiveSHA256 == entry.OriginArchiveSHA256 || entry.TreeSHA256 == entry.OriginTreeSHA256 {
		t.Fatalf("patched artifact did not change: %#v", entry)
	}
	packageFiles, err := skillpackage.ReadZip(filepath.Join(opts.Output, filepath.FromSlash(entry.PackageFile)))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(packageFiles.Files["script.py"]); got != "print('patched')\n" {
		t.Fatalf("patched script = %q", got)
	}

	opts.Output = filepath.Join(root, "runtime-frozen", "builtin-skills")
	opts.FrozenLockfile = true
	if err := run(context.Background(), opts, http.DefaultClient); err != nil {
		t.Fatalf("frozen patched build failed: %v", err)
	}

	payload := filepath.Join(root, "patches", resolvedSkillUID(spec), "fix-script-v1", "files", "script.py")
	if err := os.WriteFile(payload, []byte("print('drifted')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts.Output = filepath.Join(root, "runtime-frozen-drift", "builtin-skills")
	if err := run(context.Background(), opts, http.DefaultClient); err == nil {
		t.Fatal("frozen build accepted changed patch payload")
	}
}

func TestRunAppliesPatchToBundledSkill(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"SKILL.md":            []byte("---\nname: local-demo\ndescription: bundled skill\n---\n# Demo\n"),
		"references/guide.md": []byte("old guide\n"),
	}
	skillDir := filepath.Join(root, "research", "local-demo")
	for path, content := range files {
		filePath := filepath.Join(skillDir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSinglePatch(t, root, "bsk_local_demo", "1.0.0", files, "references/guide.md", "patched guide\n")
	sources := filepath.Join(root, "sources.yaml")
	if err := os.WriteFile(sources, []byte(`schema_version: 1
patch_catalog: patches/catalog.yaml
bundled_skills:
  - uid: bsk_local_demo
    path: research/local-demo
    category: research
    version: 1.0.0
skills: []
`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := options{
		Sources: sources,
		Lock:    filepath.Join(root, "lock.json"),
		Cache:   filepath.Join(root, "cache"),
		Output:  filepath.Join(root, "runtime", "builtin-skills"),
	}
	if err := run(context.Background(), opts, http.DefaultClient); err != nil {
		t.Fatal(err)
	}
	catalog := readCatalog(t, filepath.Join(opts.Output, "catalog.json"))
	entry := catalog.Skills[0]
	packageFiles, err := skillpackage.ReadZip(filepath.Join(opts.Output, filepath.FromSlash(entry.PackageFile)))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(packageFiles.Files["references/guide.md"]); got != "patched guide\n" || len(entry.AppliedPatches) != 1 {
		t.Fatalf("patched guide = %q, provenance = %#v", got, entry.AppliedPatches)
	}
	opts.Output = filepath.Join(root, "runtime-frozen", "builtin-skills")
	opts.FrozenLockfile = true
	if err := run(context.Background(), opts, http.DefaultClient); err != nil {
		t.Fatalf("frozen bundled patch build failed: %v", err)
	}
}

func TestRunCanPatchInvalidSkillMetadataBeforeStrictInspection(t *testing.T) {
	files := map[string][]byte{
		"SKILL.md": []byte("---\nname: broken\n---\n# Broken\n"),
	}
	archive := makeSkillZipFromFiles(t, files)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), ContentLength: int64(len(archive)), Header: make(http.Header)}, nil
	})}
	root := t.TempDir()
	sourceURL := "https://example.test/broken.zip"
	spec, err := resolveSource(sourceURL)
	if err != nil {
		t.Fatal(err)
	}
	originTree := skillpackage.TreeHash(files)
	version := "0.0.0+" + originTree[:12]
	writeSinglePatch(t, root, resolvedSkillUID(spec), version, files, "SKILL.md", "---\nname: repaired\ndescription: repaired skill\nversion: 1.0.0\n---\n# Repaired\n")
	sources := filepath.Join(root, "sources.yaml")
	if err := os.WriteFile(sources, []byte("schema_version: 1\npatch_catalog: patches/catalog.yaml\nskills:\n  - "+sourceURL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := options{Sources: sources, Lock: filepath.Join(root, "lock.json"), Cache: filepath.Join(root, "cache"), Output: filepath.Join(root, "runtime", "builtin-skills")}
	if err := run(context.Background(), opts, client); err != nil {
		t.Fatal(err)
	}
	catalog := readCatalog(t, filepath.Join(opts.Output, "catalog.json"))
	if len(catalog.Skills) != 1 || catalog.Skills[0].Name != "repaired" || catalog.Skills[0].Version != "1.0.0" {
		t.Fatalf("repaired catalog = %#v", catalog)
	}
	entry := catalog.Skills[0]
	patched, err := skillpackage.ReadZip(filepath.Join(opts.Output, filepath.FromSlash(entry.PackageFile)))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := skillmetadata.ParseRequired(patched.Files["SKILL.md"])
	if err != nil || meta.Name != "repaired" || meta.Description != "repaired skill" {
		t.Fatalf("patched SKILL.md metadata = %#v, err=%v", meta, err)
	}
	if string(files["SKILL.md"]) != "---\nname: broken\n---\n# Broken\n" || entry.OriginTreeSHA256 != originTree || len(entry.AppliedPatches) != 1 {
		t.Fatalf("source or patch provenance changed: source=%q catalog=%#v", files["SKILL.md"], entry)
	}
}

func TestRunFallsBackMetadataWithoutSkillMDPatchAndFreezes(t *testing.T) {
	files := map[string][]byte{
		"SKILL.md": []byte("---\nversion: 1.2.3\n---\n# Demo\n\nUseful bundled description.\n"),
	}
	archive := makeSkillZipFromFiles(t, files)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), ContentLength: int64(len(archive)), Header: make(http.Header)}, nil
	})}
	root := t.TempDir()
	sourceURL := "https://example.test/demo.zip"
	sources := filepath.Join(root, "sources.yaml")
	if err := os.WriteFile(sources, []byte("schema_version: 1\nskills:\n  - "+sourceURL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := options{Sources: sources, Lock: filepath.Join(root, "lock.json"), Cache: filepath.Join(root, "cache"), Output: filepath.Join(root, "runtime", "builtin-skills")}
	if err := run(context.Background(), opts, client); err != nil {
		t.Fatal(err)
	}
	normal := readCatalog(t, filepath.Join(opts.Output, "catalog.json")).Skills[0]
	originHash := sha256.Sum256(archive)
	if normal.Name != "demo" || normal.Description != "Useful bundled description." || normal.ArchiveSHA256 != hex.EncodeToString(originHash[:]) || normal.TreeSHA256 != skillpackage.TreeHash(files) || len(normal.AppliedPatches) != 0 {
		t.Fatalf("fallback catalog = %#v", normal)
	}
	meta, err := skillmetadata.ParseRequired([]byte(normal.Content))
	if err != nil || meta.Name != normal.Name || meta.Description != normal.Description {
		t.Fatalf("catalog runtime content = %q, metadata=%#v, err=%v", normal.Content, meta, err)
	}
	pkg, err := skillpackage.ReadZip(filepath.Join(opts.Output, filepath.FromSlash(normal.PackageFile)))
	if err != nil || !bytes.Equal(pkg.Files["SKILL.md"], files["SKILL.md"]) {
		t.Fatalf("packaged SKILL.md changed: pkg=%#v err=%v", pkg, err)
	}

	opts.Output = filepath.Join(root, "runtime-frozen", "builtin-skills")
	opts.FrozenLockfile = true
	if err := run(context.Background(), opts, http.DefaultClient); err != nil {
		t.Fatalf("frozen fallback build failed: %v", err)
	}
	frozen := readCatalog(t, filepath.Join(opts.Output, "catalog.json")).Skills[0]
	if frozen.Name != normal.Name || frozen.Description != normal.Description || frozen.Content != normal.Content || frozen.ArchiveSHA256 != normal.ArchiveSHA256 || frozen.TreeSHA256 != normal.TreeSHA256 {
		t.Fatalf("frozen catalog = %#v, normal = %#v", frozen, normal)
	}
}

func TestRunRejectsInvalidSkillMDPatchInsteadOfFallingBack(t *testing.T) {
	files := map[string][]byte{"SKILL.md": []byte("---\nname: original\n---\n# Demo\n\nUseful description.\n")}
	archive := makeSkillZipFromFiles(t, files)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), ContentLength: int64(len(archive)), Header: make(http.Header)}, nil
	})}
	root := t.TempDir()
	sourceURL := "https://example.test/invalid-patch.zip"
	spec, err := resolveSource(sourceURL)
	if err != nil {
		t.Fatal(err)
	}
	version := "0.0.0+" + skillpackage.TreeHash(files)[:12]
	writeSinglePatch(t, root, resolvedSkillUID(spec), version, files, "SKILL.md", "---\nname: patched\n---\n# Patched\n")
	sources := filepath.Join(root, "sources.yaml")
	if err := os.WriteFile(sources, []byte("schema_version: 1\npatch_catalog: patches/catalog.yaml\nskills:\n  - "+sourceURL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = run(context.Background(), options{Sources: sources, Lock: filepath.Join(root, "lock.json"), Cache: filepath.Join(root, "cache"), Output: filepath.Join(root, "runtime", "builtin-skills")}, client)
	if err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("invalid SKILL.md patch error = %v", err)
	}
}

func TestRunUsesFeaturedDefinitionIDForFallbackName(t *testing.T) {
	files := map[string][]byte{"SKILL.md": []byte("---\nversion: 1.2.3\n---\n# Demo\n\nFeatured fallback description.\n")}
	archive := makeSkillZipFromFiles(t, files)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), ContentLength: int64(len(archive)), Header: make(http.Header)}, nil
	})}
	root := t.TempDir()
	featuredSources := filepath.Join(root, "featured")
	featuredDir := filepath.Join(featuredSources, "demo")
	writeTestPNG(t, filepath.Join(featuredDir, "assets", "cover.png"))
	definition := strings.Replace(testFeaturedDefinition("https://example.test/skill.zip", "1.2.3"), "id: demo-featured", "id: demo", 1)
	if err := os.WriteFile(filepath.Join(featuredDir, "featured.yaml"), []byte(definition), 0o644); err != nil {
		t.Fatal(err)
	}
	sources := filepath.Join(root, "sources.yaml")
	if err := os.WriteFile(sources, []byte("schema_version: 1\nskills: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := options{Sources: sources, Lock: filepath.Join(root, "lock.json"), Cache: filepath.Join(root, "cache"), Output: filepath.Join(root, "runtime", "builtin-skills"), FeaturedSources: featuredSources, FeaturedOutput: filepath.Join(root, "runtime", "featured-skills")}
	if err := run(context.Background(), opts, client); err != nil {
		t.Fatal(err)
	}
	entry := readCatalog(t, filepath.Join(opts.Output, "catalog.json")).Skills[0]
	if entry.Name != "demo" || entry.Category != "Demo" {
		t.Fatalf("featured fallback name/category = %q/%q", entry.Name, entry.Category)
	}
}

func TestRunBuildsFeaturedCatalogAndKeepsSkillOutOfMarket(t *testing.T) {
	archive := makeSkillZip(t)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(bytes.NewReader(archive)),
			ContentLength: int64(len(archive)),
			Header:        make(http.Header),
		}, nil
	})}
	root := t.TempDir()
	sources := filepath.Join(root, "sources.yaml")
	featuredSources := filepath.Join(root, "featured")
	featuredDir := filepath.Join(featuredSources, "demo-featured")
	if err := os.MkdirAll(featuredDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, filepath.Join(featuredDir, "assets", "cover.png"))
	if err := os.WriteFile(sources, []byte("schema_version: 1\nskills: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	definition := testFeaturedDefinition("https://example.test/demo.zip", "1.2.3")
	if err := os.WriteFile(filepath.Join(featuredDir, "featured.yaml"), []byte(definition), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := options{
		Sources:         sources,
		Lock:            filepath.Join(root, "lock.json"),
		Cache:           filepath.Join(root, "cache"),
		Output:          filepath.Join(root, "runtime", "builtin-skills"),
		FeaturedSources: featuredSources,
		FeaturedOutput:  filepath.Join(root, "runtime", "featured-skills"),
	}
	if err := run(context.Background(), opts, client); err != nil {
		t.Fatal(err)
	}
	builtinCatalog := readCatalog(t, filepath.Join(opts.Output, "catalog.json"))
	if len(builtinCatalog.Skills) != 1 || builtinCatalog.Skills[0].Category != "Demo" || skillbuiltin.CatalogSkillMarketVisible(builtinCatalog.Skills[0]) {
		t.Fatalf("builtin catalog = %#v", builtinCatalog)
	}
	body, err := os.ReadFile(filepath.Join(opts.FeaturedOutput, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var featuredCatalog showcase.Catalog
	if err := json.Unmarshal(body, &featuredCatalog); err != nil {
		t.Fatal(err)
	}
	if len(featuredCatalog.Cases) != 1 || featuredCatalog.Cases[0].Skill.BuiltinSkillUID != builtinCatalog.Skills[0].UID {
		t.Fatalf("featured catalog = %#v", featuredCatalog)
	}
	asset := featuredCatalog.Cases[0].Assets["cover"]
	if asset.URL == "" || asset.SHA256 == "" {
		t.Fatalf("compiled asset = %#v", asset)
	}
}

func TestRunBuildsFeaturedCatalogFromLocalDirectory(t *testing.T) {
	root := t.TempDir()
	featuredSources := filepath.Join(root, "featured")
	featuredDir := filepath.Join(featuredSources, "demo-featured")
	skillDir := filepath.Join(featuredDir, "skill")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: local-featured\ndescription: local featured skill\nversion: 1.2.3\n---\n# Local Featured\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	referencePath := filepath.Join(skillDir, "references", "guide.md")
	if err := os.WriteFile(referencePath, []byte("guide\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, filepath.Join(featuredDir, "assets", "cover.png"))
	if err := os.WriteFile(filepath.Join(featuredDir, "featured.yaml"), []byte(testFeaturedDefinition("featured/demo-featured/skill", "1.2.3")), 0o644); err != nil {
		t.Fatal(err)
	}
	sources := filepath.Join(root, "sources.yaml")
	if err := os.WriteFile(sources, []byte("schema_version: 1\nskills: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := options{
		Sources:         sources,
		Lock:            filepath.Join(root, "lock.json"),
		Cache:           filepath.Join(root, "cache"),
		Output:          filepath.Join(root, "runtime", "builtin-skills"),
		FeaturedSources: featuredSources,
		FeaturedOutput:  filepath.Join(root, "runtime", "featured-skills"),
	}
	if err := run(context.Background(), opts, http.DefaultClient); err != nil {
		t.Fatal(err)
	}
	builtinCatalog := readCatalog(t, filepath.Join(opts.Output, "catalog.json"))
	if len(builtinCatalog.Skills) != 1 {
		t.Fatalf("builtin catalog = %#v", builtinCatalog)
	}
	entry := builtinCatalog.Skills[0]
	if entry.SourceURL != "builtin://featured/demo-featured/skill" || entry.Category != "Demo" || skillbuiltin.CatalogSkillMarketVisible(entry) {
		t.Fatalf("local featured entry = %#v", entry)
	}
	packageFiles, err := skillpackage.ReadZip(filepath.Join(opts.Output, filepath.FromSlash(entry.PackageFile)))
	if err != nil {
		t.Fatal(err)
	}
	if string(packageFiles.Files["references/guide.md"]) != "guide\n" {
		t.Fatalf("package files = %#v", packageFiles)
	}
	featuredCatalog := readFeaturedCatalog(t, filepath.Join(opts.FeaturedOutput, "catalog.json"))
	if len(featuredCatalog.Cases) != 1 || featuredCatalog.Cases[0].Skill.SourceURL != entry.SourceURL || featuredCatalog.Cases[0].Skill.BuiltinSkillUID != entry.UID {
		t.Fatalf("featured catalog = %#v", featuredCatalog)
	}

	opts.Output = filepath.Join(root, "runtime-frozen", "builtin-skills")
	opts.FeaturedOutput = filepath.Join(root, "runtime-frozen", "featured-skills")
	opts.FrozenLockfile = true
	if err := run(context.Background(), opts, http.DefaultClient); err != nil {
		t.Fatalf("frozen local featured build failed: %v", err)
	}
	if err := os.WriteFile(referencePath, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts.Output = filepath.Join(root, "runtime-changed", "builtin-skills")
	opts.FeaturedOutput = filepath.Join(root, "runtime-changed", "featured-skills")
	if err := run(context.Background(), opts, http.DefaultClient); err != nil {
		t.Fatalf("frozen build rejected current local Featured Skill source: %v", err)
	}
	changedEntry := readCatalog(t, filepath.Join(opts.Output, "catalog.json")).Skills[0]
	if changedEntry.TreeSHA256 == entry.TreeSHA256 {
		t.Fatal("changed local Featured Skill did not produce a new runtime tree hash")
	}
}

func TestRunRejectsFeaturedSourceAssignedToSkillMarket(t *testing.T) {
	root := t.TempDir()
	featuredSources := filepath.Join(root, "featured")
	featuredDir := filepath.Join(featuredSources, "demo-featured")
	skillDir := filepath.Join(featuredDir, "skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: local-featured\ndescription: local featured skill\nversion: 1.2.3\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, filepath.Join(featuredDir, "assets", "cover.png"))
	if err := os.WriteFile(filepath.Join(featuredDir, "featured.yaml"), []byte(testFeaturedDefinition("featured/demo-featured/skill", "1.2.3")), 0o644); err != nil {
		t.Fatal(err)
	}
	sources := filepath.Join(root, "sources.yaml")
	if err := os.WriteFile(sources, []byte(`schema_version: 1
bundled_skills:
  - uid: bsk_market_demo
    path: featured/demo-featured/skill
    category: demo
    version: 1.2.3
skills: []
`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := options{
		Sources: sources, Lock: filepath.Join(root, "lock.json"), Cache: filepath.Join(root, "cache"),
		Output: filepath.Join(root, "runtime", "builtin-skills"), FeaturedSources: featuredSources,
		FeaturedOutput: filepath.Join(root, "runtime", "featured-skills"),
	}
	err := run(context.Background(), opts, http.DefaultClient)
	if err == nil || !strings.Contains(err.Error(), "cannot be both a market Skill and a featured-only Skill") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRejectsMissingLocalFeaturedDirectory(t *testing.T) {
	root := t.TempDir()
	featuredSources := filepath.Join(root, "featured")
	featuredDir := filepath.Join(featuredSources, "demo-featured")
	writeTestPNG(t, filepath.Join(featuredDir, "assets", "cover.png"))
	if err := os.WriteFile(filepath.Join(featuredDir, "featured.yaml"), []byte(testFeaturedDefinition("featured/demo-featured/missing", "1.2.3")), 0o644); err != nil {
		t.Fatal(err)
	}
	sources := filepath.Join(root, "sources.yaml")
	if err := os.WriteFile(sources, []byte("schema_version: 1\nskills: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), options{
		Sources: sources, Lock: filepath.Join(root, "lock.json"), Cache: filepath.Join(root, "cache"),
		Output: filepath.Join(root, "runtime", "builtin-skills"), FeaturedSources: featuredSources,
		FeaturedOutput: filepath.Join(root, "runtime", "featured-skills"),
	}, http.DefaultClient)
	if err == nil || !strings.Contains(err.Error(), "bundled skill directory not found") {
		t.Fatalf("error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testFeaturedDefinition(source, requiredVersion string) string {
	return `schema_version: 2
id: demo-featured
type: work
version: 1.0.0
status: published
default_locale: zh-CN
provider: LazyMind
skill:
  source_url: ` + source + `
  category: Demo
  required_version: ` + requiredVersion + `
placement:
  home: true
  gallery: true
  order: 9
classification:
  category: Demo
assets:
  cover:
    file: assets/cover.png
    role: cover
presentation:
  card:
    title: Demo
    description: Demo description
    output_type: report
    output_label: Report
    cover_asset: cover
    result_summary: Summary
  detail:
    title: Demo detail
    description: Demo detail description
tasks:
  - id: plan
    selector:
      title: Plan
      description: Plan description
      output_label: Report
    launch:
      prompt_short: User task
      prompt: Run task
    replay:
      steps:
        - title: Analyze
          description: Analyze inputs
    result:
      template: generic_report_v1
      eyebrow: Report
      title: Demo result
      summary: Result
      highlights: [One]
`
}

func readFeaturedCatalog(t *testing.T, path string) showcase.Catalog {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var catalog showcase.Catalog
	if err := json.Unmarshal(body, &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog
}

func makeSkillZip(t *testing.T) []byte {
	t.Helper()
	return makeSkillZipFromFiles(t, testSkillFiles())
}

func makeSkillZipWithVersion(t *testing.T, version string) []byte {
	t.Helper()
	files := testSkillFiles()
	files["_meta.json"] = []byte(fmt.Sprintf(`{"version":%q}`, version))
	return makeSkillZipFromFiles(t, files)
}

func testSkillFiles() map[string][]byte {
	return map[string][]byte{
		"SKILL.md":  []byte("---\nname: demo\ndescription: demo skill\nversion: 1.2.3\ntags: [test]\n---\n# Demo\n"),
		"script.py": []byte("print('ok')\n"),
	}
}

func makeSkillZipFromFiles(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skill.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write(content)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func writeSinglePatch(t *testing.T, root, uid, version string, files map[string][]byte, targetPath, replacement string) {
	t.Helper()
	patchRelative := filepath.ToSlash(filepath.Join(uid, "fix-script-v1", "patch.yaml"))
	catalogPath := filepath.Join(root, "patches", "catalog.yaml")
	if err := os.MkdirAll(filepath.Dir(catalogPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, []byte("schema_version: 1\npatches:\n  - "+patchRelative+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := sha256.Sum256(files[targetPath])
	definition := `schema_version: 1
id: ` + uid + `/fix-script-v1
target:
  uid: ` + uid + `
  version: ` + version + `
  origin_tree_sha256: ` + skillpackage.TreeHash(files) + `
operations:
  - op: upsert
    path: ` + targetPath + `
    file: files/` + targetPath + `
    before_sha256: ` + hex.EncodeToString(before[:]) + "\n"
	patchRoot := filepath.Join(root, "patches", uid, "fix-script-v1")
	if err := os.MkdirAll(filepath.Join(patchRoot, "files", filepath.Dir(filepath.FromSlash(targetPath))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(patchRoot, "patch.yaml"), []byte(definition), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(patchRoot, "files", filepath.FromSlash(targetPath)), []byte(replacement), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readCatalog(t *testing.T, path string) skillbuiltin.Catalog {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var catalog skillbuiltin.Catalog
	if err := json.Unmarshal(body, &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog
}

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		for y := 0; y < 64; y++ {
			img.Set(x, y, color.RGBA{R: 32, G: 96, B: 192, A: 255})
		}
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
