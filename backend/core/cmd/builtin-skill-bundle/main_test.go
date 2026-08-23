package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"lazymind/core/showcase"
	skillbuiltin "lazymind/core/skillv2/builtin"
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
	if len(catalog.Skills) != 1 || catalog.Skills[0].UID != "bsk_local_demo" || catalog.Skills[0].SourceURL != "builtin://research/local-demo" || !skillbuiltin.CatalogSkillMarketVisible(catalog.Skills[0]) {
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
	if err := run(context.Background(), opts, http.DefaultClient); err == nil {
		t.Fatal("frozen build accepted a changed bundled Skill")
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
	definition := `schema_version: 2
id: demo-featured
type: work
version: 1.0.0
status: published
default_locale: zh-CN
skill:
  source_url: https://example.test/demo.zip
  required_version: 1.2.3
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
	if len(builtinCatalog.Skills) != 1 || skillbuiltin.CatalogSkillMarketVisible(builtinCatalog.Skills[0]) {
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func makeSkillZip(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skill.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range map[string]string{
		"SKILL.md":  "---\nname: demo\ndescription: demo skill\nversion: 1.2.3\ntags: [test]\n---\n# Demo\n",
		"script.py": "print('ok')\n",
	} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write([]byte(content))
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
