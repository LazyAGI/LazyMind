package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func genericOptionsFromYAML(t *testing.T, text string) Options {
	t.Helper()
	var values map[string]any
	if err := yaml.Unmarshal([]byte(text), &values); err != nil {
		t.Fatalf("decode options YAML: %v", err)
	}
	return Options(values)
}

func writeGenericFixture(t *testing.T, root, relativePath, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestGenericFileCopyGroups(t *testing.T) {
	root := t.TempDir()
	writeGenericFixture(t, root, "translations/zh-CN/00-course/README.md", "中文")
	writeGenericFixture(t, root, "00-course/README.md", "english")
	writeGenericFixture(t, root, "00-course/image.png", "image")

	adapter, err := New(AdapterGenericFileCopy, genericOptionsFromYAML(t, `
extensions: [".md"]
groups:
  - name: zh-CN
    match:
      - '^translations/zh-CN/.*\.md$'
    strip_prefix: "translations/zh-CN/"
    display_name_template: "zh-CN/{{path}}"
    relative_path_template: "zh-CN/{{dir}}"
    tags: ["generative-ai", "zh-CN"]
  - name: en-original
    match:
      - '^00-course/.*\.md$'
    display_name_template: "{{path}}"
    relative_path_template: "{{dir}}"
    tags: ["generative-ai", "en-original"]
`))
	if err != nil {
		t.Fatalf("new generic_file_copy: %v", err)
	}

	files := []FileEntry{
		{Path: "translations/zh-CN/00-course/README.md", Size: 6},
		{Path: "00-course/README.md", Size: 7},
		{Path: "00-course/image.png", Size: 5},
	}
	units, err := adapter.Materialize(context.Background(), root, files)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("expected 2 units, got %d: %+v", len(units), units)
	}

	var zh, en *IngestUnit
	for index := range units {
		unit := &units[index]
		if strings.Contains(unit.DisplayName, "zh-CN/") {
			zh = unit
		} else {
			en = unit
		}
	}
	if zh == nil || zh.DisplayName != "zh-CN/00-course/README.md" || zh.RelativePath != "zh-CN/00-course" {
		t.Fatalf("unexpected zh-CN unit: %+v", zh)
	}
	if len(zh.Tags) != 2 || zh.Tags[1] != "zh-CN" {
		t.Fatalf("unexpected zh-CN tags: %v", zh.Tags)
	}
	if en == nil || en.DisplayName != "00-course/README.md" || en.RelativePath != "00-course" {
		t.Fatalf("unexpected en unit: %+v", en)
	}
	if len(en.Tags) != 2 || en.Tags[1] != "en-original" {
		t.Fatalf("unexpected en tags: %v", en.Tags)
	}
}

func TestGenericMarkdownFrontMatter(t *testing.T) {
	root := t.TempDir()
	writeGenericFixture(t, root, "content/法律/test.md", `---
LinkTitle: 中华人民共和国民法典
title: 民法典
status: 有效
group: 法律
---
# 中华人民共和国民法典

正文
`)

	adapter, err := New(AdapterGenericMarkdown, genericOptionsFromYAML(t, `
include:
  - '^content/(法律|行政法规|司法解释|监察法规|宪法|appendix)/[^/]+\.md$'
exclude:
  - '_index\.md$'
front_matter: true
title_fields: ["LinkTitle", "title"]
heading_fallback: false
title_fallback_template: "{{name}}"
group_field: group
group_fallback_template: "{{seg1}}"
status_field: status
status_map:
  "有效": "有效"
  "生效": "有效"
  "": "未知"
strip_front_matter: false
display_name_template: "{{title}}.md"
relative_path_template: "{{group}}/{{status}}"
tags: ["lawtext", "{{group}}", "{{status}}"]
`))
	if err != nil {
		t.Fatalf("new generic_markdown: %v", err)
	}

	units, err := adapter.Materialize(context.Background(), root, []FileEntry{{Path: "content/法律/test.md"}})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(units))
	}
	unit := units[0]
	if unit.DisplayName != "中华人民共和国民法典.md" {
		t.Fatalf("unexpected display name %q", unit.DisplayName)
	}
	if unit.RelativePath != "法律/有效" {
		t.Fatalf("unexpected relative path %q", unit.RelativePath)
	}
	if len(unit.Tags) != 3 || unit.Tags[1] != "法律" || unit.Tags[2] != "有效" {
		t.Fatalf("unexpected tags %v", unit.Tags)
	}
	if adapter.Match("content/法律/_index.md") {
		t.Fatal("expected _index.md to be excluded by Match")
	}
}

func TestGenericTabularCSVRows(t *testing.T) {
	root := t.TempDir()
	writeGenericFixture(t, root, "data.csv", "title,effect_level,content\n第一条,规范性文件,正文一\n第二条,,正文二\n")

	adapter, err := New(AdapterGenericTabular, genericOptionsFromYAML(t, `
input_extensions: [".csv"]
doc_mode: row
title_fields: ["title"]
title_fallback_template: "document_{{seq}}"
field_defaults:
  effect_level: "其他"
document_template: |
  # {{title}}

  {{content}}
display_name_template: "{{title}}.md"
relative_path_template: "税务政策/{{effect_level}}"
tags: ["chinatax", "{{effect_level}}"]
`))
	if err != nil {
		t.Fatalf("new generic_tabular: %v", err)
	}

	units, err := adapter.Materialize(context.Background(), root, []FileEntry{{Path: "data.csv"}})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("expected 2 units, got %d", len(units))
	}
	if units[0].DisplayName != "第一条.md" || units[1].DisplayName != "第二条.md" {
		t.Fatalf("unexpected display names: %+v", units)
	}
	if units[1].RelativePath != "税务政策/其他" {
		t.Fatalf("expected default effect_level, got %q", units[1].RelativePath)
	}
	content, err := os.ReadFile(units[1].LocalPath)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	if !strings.Contains(string(content), "# 第二条") || !strings.Contains(string(content), "正文二") {
		t.Fatalf("unexpected generated content %q", string(content))
	}
}

func TestGenericAdapterRejectsUnknownOption(t *testing.T) {
	_, err := New(AdapterGenericFileCopy, genericOptionsFromYAML(t, `
extensions: [".md"]
unknown_field: true
`))
	if err == nil || !strings.Contains(err.Error(), "unknown_field") {
		t.Fatalf("expected unknown option error, got %v", err)
	}
}

func TestGenericAdapterRejectsInvalidPattern(t *testing.T) {
	_, err := New(AdapterGenericFileCopy, genericOptionsFromYAML(t, `
include:
  - '['
`))
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("expected invalid pattern error, got %v", err)
	}
}

func TestGenericAdapterIDsRegistered(t *testing.T) {
	ids := RegisteredIDs()
	joined := strings.Join(ids, ",")
	for _, id := range []string{AdapterGenericFileCopy, AdapterGenericMarkdown, AdapterGenericTabular} {
		if !strings.Contains(joined, id) {
			t.Fatalf("expected %s to be registered, got %v", id, ids)
		}
	}
	for _, id := range []string{AdapterLawtextLaws, AdapterJustLaws, AdapterChinataxPolicy, AdapterGenAIBeginners} {
		if !strings.Contains(joined, id) {
			t.Fatalf("expected existing specialized adapter %s to remain registered, got %v", id, ids)
		}
	}
}
