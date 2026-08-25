package showcase

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileCatalogBuildsLocalizedCasesAndHashedAssets(t *testing.T) {
	root := t.TempDir()
	writeFeaturedSource(t, root, "multi", validFeaturedYAML("multi", true))
	writeLocale(t, root, "multi", "en-US", validLocaleYAML(true))
	definitions, err := LoadSourceDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 1 || len(definitions[0].Tasks) != 2 || len(definitions[0].Locales["en-US"].Tasks) != 2 {
		t.Fatalf("definitions = %#v", definitions)
	}
	definitions[0].Skill.BuiltinSkillUID = "bsk_demo"
	definitions[0].Skill.Version = "1.0.0"
	definitions[0].Skill.ArchiveSHA256 = strings.Repeat("a", 64)
	output := filepath.Join(t.TempDir(), "featured-skills")
	catalog, err := CompileCatalog(definitions, output)
	if err != nil {
		t.Fatal(err)
	}
	asset := catalog.Cases[0].Assets["cover"]
	if !strings.HasPrefix(asset.URL, "/showcase-assets/multi/1.0.0/") || asset.Width != 64 || asset.Height != 64 || asset.MIME != "image/png" {
		t.Fatalf("asset = %#v", asset)
	}
	if _, err := os.Stat(filepath.Join(output, "assets", "multi", "1.0.0", filepath.Base(asset.URL))); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(output, "catalog.json")
	if err := os.WriteFile(catalogPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCatalog(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := loaded.ShowcaseCases("en-US")
	if len(cases) != 1 || cases[0].Type != TypeChat || cases[0].Title != "English demo" || cases[0].DetailTitle != "English detail" || cases[0].Category != "Education" || len(cases[0].Tasks) != 2 {
		t.Fatalf("cases = %#v", cases)
	}
	if cases[0].BuiltinSkillUID != "bsk_demo" || cases[0].SourceURL != "https://example.test/multi.zip" {
		t.Fatalf("featured case is not bound to its configured Skill: %#v", cases[0])
	}
	if cases[0].Tasks[1].Result.Title != "English result" || cases[0].Tasks[1].Steps[0].Title != "Analyze" {
		t.Fatalf("task = %#v", cases[0].Tasks[1])
	}
}

func TestFeaturedCatalogPathCandidatesCoverLocalAndDesktopLayouts(t *testing.T) {
	localWorkingDirectory := filepath.Join(string(filepath.Separator), "repo", "backend", "core")
	local := featuredCatalogPathCandidates(localWorkingDirectory)
	if got, want := filepath.Clean(local[1]), filepath.Join(string(filepath.Separator), "repo", "skills", ".runtime", "featured-skills", "catalog.json"); got != want {
		t.Fatalf("local catalog path = %q, want %q", got, want)
	}
	desktopWorkingDirectory := filepath.Join(string(filepath.Separator), "resources", "runtime", "app", "backend", "core")
	desktop := featuredCatalogPathCandidates(desktopWorkingDirectory)
	if got, want := filepath.Clean(desktop[2]), filepath.Join(string(filepath.Separator), "resources", "runtime", "featured-skills", "catalog.json"); got != want {
		t.Fatalf("desktop catalog path = %q, want %q", got, want)
	}
}

func TestShowcaseCasesUseConfiguredPlacementOrder(t *testing.T) {
	root := t.TempDir()
	writeFeaturedSource(t, root, "first", strings.Replace(validFeaturedYAML("first", false), "order: 1", "order: 2", 1))
	writeFeaturedSource(t, root, "second", validFeaturedYAML("second", false))
	definitions, err := LoadSourceDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	for index := range definitions {
		definitions[index].Skill.BuiltinSkillUID = "bsk_" + definitions[index].ID
		definitions[index].Skill.Version = "1.0.0"
		definitions[index].Skill.ArchiveSHA256 = strings.Repeat("a", 64)
	}
	catalog, err := CompileCatalog(definitions, filepath.Join(t.TempDir(), "featured-skills"))
	if err != nil {
		t.Fatal(err)
	}
	cases := catalog.ShowcaseCases("zh-CN")
	if len(cases) != 2 || cases[0].ID != "second" || cases[1].ID != "first" {
		t.Fatalf("cases are not ordered by placement.order: %#v", cases)
	}
}

func TestLoadSourceDirectoryRejectsUnknownFieldsAndInvalidAssets(t *testing.T) {
	t.Run("unknown field", func(t *testing.T) {
		root := t.TempDir()
		writeFeaturedSource(t, root, "demo", validFeaturedYAML("demo", false)+"unknown_field: true\n")
		_, err := LoadSourceDirectory(root)
		if err == nil || !strings.Contains(err.Error(), "field unknown_field not found") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("asset traversal", func(t *testing.T) {
		root := t.TempDir()
		body := strings.Replace(validFeaturedYAML("demo", false), "file: assets/cover.png", "file: ../cover.png", 1)
		writeFeaturedSource(t, root, "demo", body)
		_, err := LoadSourceDirectory(root)
		if err == nil || !strings.Contains(err.Error(), "under assets/") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("missing asset", func(t *testing.T) {
		root := t.TempDir()
		body := strings.Replace(validFeaturedYAML("demo", false), "assets/cover.png", "assets/missing.png", 1)
		writeFeaturedSource(t, root, "demo", body)
		_, err := LoadSourceDirectory(root)
		if err == nil || !strings.Contains(err.Error(), "missing.png") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("invalid MIME", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "demo")
		if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "assets", "cover.png"), []byte("not an image"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "featured.yaml"), []byte(validFeaturedYAML("demo", false)), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadSourceDirectory(root)
		if err == nil || !strings.Contains(err.Error(), "unsupported MIME") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("invalid output type", func(t *testing.T) {
		root := t.TempDir()
		body := strings.Replace(validFeaturedYAML("demo", false), "output_type: report", "output_type: executable", 1)
		writeFeaturedSource(t, root, "demo", body)
		_, err := LoadSourceDirectory(root)
		if err == nil || !strings.Contains(err.Error(), "unsupported output_type") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("invalid placement order", func(t *testing.T) {
		root := t.TempDir()
		body := strings.Replace(validFeaturedYAML("demo", false), "order: 1", "order: 0", 1)
		writeFeaturedSource(t, root, "demo", body)
		_, err := LoadSourceDirectory(root)
		if err == nil || !strings.Contains(err.Error(), "placement.order must be positive") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("invalid type", func(t *testing.T) {
		root := t.TempDir()
		body := strings.Replace(validFeaturedYAML("demo", false), "type: chat", "type: agent", 1)
		writeFeaturedSource(t, root, "demo", body)
		_, err := LoadSourceDirectory(root)
		if err == nil || !strings.Contains(err.Error(), "type must be chat or work") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestLoadSourceDirectoryRejectsLocaleTaskDrift(t *testing.T) {
	root := t.TempDir()
	writeFeaturedSource(t, root, "demo", validFeaturedYAML("demo", true))
	writeLocale(t, root, "demo", "en-US", validLocaleYAML(false))
	_, err := LoadSourceDirectory(root)
	if err == nil || !strings.Contains(err.Error(), "task ids must match") {
		t.Fatalf("error = %v", err)
	}
}

func TestProductReportTemplateRequiresConfiguredSlots(t *testing.T) {
	root := t.TempDir()
	body := strings.Replace(validFeaturedYAML("demo", false), "template: generic_report_v1", "template: product_report_v1", 1)
	writeFeaturedSource(t, root, "demo", body)
	_, err := LoadSourceDirectory(root)
	if err == nil || !strings.Contains(err.Error(), "product result is incomplete") {
		t.Fatalf("error = %v", err)
	}
}

func writeFeaturedSource(t *testing.T, root, id, body string) {
	t.Helper()
	dir := filepath.Join(root, id)
	writePNG(t, filepath.Join(dir, "assets", "cover.png"))
	if err := os.WriteFile(filepath.Join(dir, "featured.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeLocale(t *testing.T, root, id, locale, body string) {
	t.Helper()
	path := filepath.Join(root, id, "locales", locale+".yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePNG(t *testing.T, path string) {
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

func validFeaturedYAML(id string, secondTask bool) string {
	tasks := validTaskYAML("plan")
	if secondTask {
		tasks += validTaskYAML("compare")
	}
	return "schema_version: 2\n" +
		"id: " + id + "\n" +
		"type: chat\n" +
		"version: 1.0.0\nstatus: published\ndefault_locale: zh-CN\n" +
		"skill:\n  source_url: https://example.test/" + id + ".zip\n" +
		"placement:\n  home: true\n  gallery: true\n  order: 1\n" +
		"classification:\n  category: Demo\n  tags: [demo]\n" +
		"assets:\n  cover:\n    file: assets/cover.png\n    role: cover\n" +
		"presentation:\n  card:\n    title: Demo\n    description: Demo description\n    output_type: report\n    output_label: Report\n    cover_asset: cover\n    result_summary: Summary\n  detail:\n    title: Demo detail\n    description: Demo detail description\n" +
		"tasks:\n" + tasks
}

func validLocaleYAML(secondTask bool) string {
	tasks := localizedTaskYAML("plan")
	if secondTask {
		tasks += localizedTaskYAML("compare")
	}
	return "locale: en-US\n" +
		"classification:\n  category: Education\n  tags: [gaokao]\n" +
		"presentation:\n  card:\n    title: English demo\n    description: English description\n    output_type: report\n    output_label: Report\n    cover_asset: cover\n    result_summary: Summary\n  detail:\n    title: English detail\n    description: English detail description\n" +
		"tasks:\n" + tasks
}

func validTaskYAML(id string) string {
	return "  - id: " + id + "\n" +
		"    selector:\n      title: Plan\n      description: Plan description\n      output_label: Report\n" +
		"    launch:\n      prompt_short: User task\n      prompt: Run task\n" +
		"    replay:\n      steps:\n        - title: Analyze\n          description: Analyze inputs\n" +
		"    result:\n      template: generic_report_v1\n      eyebrow: Report\n      title: Result\n      summary: Result summary\n      highlights: [One]\n"
}

func localizedTaskYAML(id string) string {
	return strings.Replace(validTaskYAML(id), "title: Result", "title: English result", 1)
}
