package source

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const AdapterGenericMarkdown = "generic_markdown"

type genericMarkdownConfig struct {
	Include               []string          `yaml:"include"`
	Exclude               []string          `yaml:"exclude"`
	Extensions            []string          `yaml:"extensions"`
	FrontMatter           bool              `yaml:"front_matter"`
	TitleFields           []string          `yaml:"title_fields"`
	HeadingFallback       bool              `yaml:"heading_fallback"`
	TitleFallbackTemplate string            `yaml:"title_fallback_template"`
	GroupField            string            `yaml:"group_field"`
	GroupFallbackTemplate string            `yaml:"group_fallback_template"`
	StatusField           string            `yaml:"status_field"`
	StatusMap             map[string]string `yaml:"status_map"`
	StripFrontMatter      bool              `yaml:"strip_front_matter"`
	DisplayNameTemplate   string            `yaml:"display_name_template"`
	RelativePathTemplate  string            `yaml:"relative_path_template"`
	Tags                  []string          `yaml:"tags"`
	Groups                []genericGroup    `yaml:"groups"`
}

type genericMarkdownAdapter struct {
	rule                  *genericMatchRule
	frontMatter           bool
	titleFields           []string
	headingFallback       bool
	titleFallbackTemplate string
	groupField            string
	groupFallbackTemplate string
	statusField           string
	statusMap             map[string]string
	stripFrontMatter      bool
	displayNameTemplate   string
	relativePathTemplate  string
	tags                  []string
}

func init() {
	Register(AdapterGenericMarkdown, newGenericMarkdownAdapter)
}

func newGenericMarkdownAdapter(options Options) (Adapter, error) {
	var config genericMarkdownConfig
	if err := decodeStrictOptions(options, &config, AdapterGenericMarkdown); err != nil {
		return nil, err
	}
	if len(config.Extensions) == 0 {
		config.Extensions = []string{".md"}
	}
	config.DisplayNameTemplate = strings.TrimSpace(config.DisplayNameTemplate)
	config.RelativePathTemplate = strings.TrimSpace(config.RelativePathTemplate)
	config.TitleFallbackTemplate = strings.TrimSpace(config.TitleFallbackTemplate)
	config.GroupFallbackTemplate = strings.TrimSpace(config.GroupFallbackTemplate)
	if config.DisplayNameTemplate == "" {
		config.DisplayNameTemplate = "{{title}}.md"
	}
	if config.RelativePathTemplate == "" {
		config.RelativePathTemplate = "{{dir}}"
	}
	if config.TitleFallbackTemplate == "" {
		config.TitleFallbackTemplate = "{{name}}"
	}
	for name, template := range map[string]string{
		"display_name_template":   config.DisplayNameTemplate,
		"relative_path_template":  config.RelativePathTemplate,
		"title_fallback_template": config.TitleFallbackTemplate,
		"group_fallback_template": config.GroupFallbackTemplate,
	} {
		if err := validateTemplateSyntax(template); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	for _, tag := range config.Tags {
		if err := validateTemplateSyntax(tag); err != nil {
			return nil, fmt.Errorf("tag template: %w", err)
		}
	}
	rule, err := newGenericMatchRule(config.Include, config.Exclude, config.Extensions, config.Groups)
	if err != nil {
		return nil, err
	}
	return &genericMarkdownAdapter{
		rule:                  rule,
		frontMatter:           config.FrontMatter,
		titleFields:           config.TitleFields,
		headingFallback:       config.HeadingFallback,
		titleFallbackTemplate: config.TitleFallbackTemplate,
		groupField:            strings.TrimSpace(config.GroupField),
		groupFallbackTemplate: config.GroupFallbackTemplate,
		statusField:           strings.TrimSpace(config.StatusField),
		statusMap:             config.StatusMap,
		stripFrontMatter:      config.StripFrontMatter,
		displayNameTemplate:   config.DisplayNameTemplate,
		relativePathTemplate:  config.RelativePathTemplate,
		tags:                  config.Tags,
	}, nil
}

func (a *genericMarkdownAdapter) ID() string { return AdapterGenericMarkdown }

func (a *genericMarkdownAdapter) Match(path string) bool {
	return a.rule.matches(path)
}

func (a *genericMarkdownAdapter) Materialize(ctx context.Context, root string, files []FileEntry) ([]IngestUnit, error) {
	generatedDir := filepath.Join(root, ".ingest_generic_markdown")
	if a.stripFrontMatter {
		if err := os.MkdirAll(generatedDir, 0o755); err != nil {
			return nil, fmt.Errorf("create generic markdown generated dir: %w", err)
		}
	}
	units := make([]IngestUnit, 0, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !a.rule.matches(file.Path) {
			continue
		}
		group, grouped := a.rule.resolveGroup(file.Path)
		groupName := ""
		displayTemplate := a.displayNameTemplate
		relativeTemplate := a.relativePathTemplate
		tags := a.tags
		if grouped {
			groupName = group.Name
			if group.DisplayNameTemplate != "" {
				displayTemplate = group.DisplayNameTemplate
			}
			if group.RelativePathTemplate != "" {
				relativeTemplate = group.RelativePathTemplate
			}
			if group.Tags != nil {
				tags = group.Tags
			}
		}
		localPath := filepath.Join(root, filepath.FromSlash(file.Path))
		frontMatter := map[string]any{}
		if a.frontMatter {
			meta, err := readGenericFrontMatter(localPath)
			if err != nil {
				return nil, fmt.Errorf("read front matter from %s: %w", file.Path, err)
			}
			frontMatter = meta
		}
		fileContext := newGenericFileContext(file, group.StripPrefix, groupName)
		values := fileContext.values()

		title, err := a.resolveTitle(localPath, frontMatter, values)
		if err != nil {
			return nil, fmt.Errorf("resolve title for %s: %w", file.Path, err)
		}
		values["title"] = title

		groupValue, err := a.resolveGroupValue(frontMatter, values)
		if err != nil {
			return nil, fmt.Errorf("resolve group for %s: %w", file.Path, err)
		}
		values["group"] = groupValue
		values["status"] = a.resolveStatus(frontMatter)

		if a.stripFrontMatter {
			strippedPath := filepath.Join(generatedDir, shortHash(file.Path)+".md")
			if err := stripGenericFrontMatter(localPath, strippedPath); err != nil {
				return nil, fmt.Errorf("strip front matter from %s: %w", file.Path, err)
			}
			localPath = strippedPath
		}

		displayName, err := renderTemplate(displayTemplate, values, false)
		if err != nil {
			return nil, fmt.Errorf("render display name for %s: %w", file.Path, err)
		}
		displayName = cleanDisplayName(displayName)
		relativePath, err := renderTemplate(relativeTemplate, values, false)
		if err != nil {
			return nil, fmt.Errorf("render relative path for %s: %w", file.Path, err)
		}
		relativePath = cleanRelativePath(relativePath)
		renderedTags, err := renderTags(tags, values, false)
		if err != nil {
			return nil, fmt.Errorf("render tags for %s: %w", file.Path, err)
		}
		units = append(units, IngestUnit{
			LocalPath:    localPath,
			DisplayName:  displayName,
			RelativePath: relativePath,
			Tags:         renderedTags,
		})
	}
	return units, nil
}

func (a *genericMarkdownAdapter) resolveTitle(localPath string, frontMatter map[string]any, values map[string]string) (string, error) {
	for _, field := range a.titleFields {
		if value := strings.TrimSpace(rowString(frontMatter[strings.TrimSpace(field)])); value != "" {
			return value, nil
		}
	}
	if a.headingFallback {
		if heading := firstMarkdownHeadingSkippingFrontMatter(localPath); heading != "" {
			return heading, nil
		}
	}
	if a.titleFallbackTemplate != "" {
		return renderTemplate(a.titleFallbackTemplate, values, false)
	}
	return values["name"], nil
}

func (a *genericMarkdownAdapter) resolveGroupValue(frontMatter map[string]any, values map[string]string) (string, error) {
	if a.groupField != "" {
		if value := strings.TrimSpace(rowString(frontMatter[a.groupField])); value != "" {
			return value, nil
		}
	}
	if a.groupFallbackTemplate != "" {
		return renderTemplate(a.groupFallbackTemplate, values, false)
	}
	return "", nil
}

func (a *genericMarkdownAdapter) resolveStatus(frontMatter map[string]any) string {
	raw := ""
	if a.statusField != "" {
		raw = strings.TrimSpace(rowString(frontMatter[a.statusField]))
	}
	if len(a.statusMap) > 0 {
		if mapped, ok := a.statusMap[raw]; ok {
			return strings.TrimSpace(mapped)
		}
	}
	return raw
}

func readGenericFrontMatter(path string) (map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return map[string]any{}, nil
	}
	if strings.TrimSpace(scanner.Text()) != "---" {
		return map[string]any{}, nil
	}
	var block []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		block = append(block, line)
	}
	if len(block) == 0 {
		return map[string]any{}, nil
	}
	meta := map[string]any{}
	if err := yaml.Unmarshal([]byte(strings.Join(block, "\n")), &meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func firstMarkdownHeadingSkippingFrontMatter(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	inFrontMatter := false
	firstLine := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if firstLine {
			firstLine = false
			if line == "---" {
				inFrontMatter = true
				continue
			}
		}
		if inFrontMatter {
			if line == "---" {
				inFrontMatter = false
			}
			continue
		}
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func stripGenericFrontMatter(src, dst string) error {
	input, err := os.Open(src)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(dst)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = os.Remove(dst)
		}
	}()

	scanner := bufio.NewScanner(input)
	inFrontMatter := false
	firstLine := true
	for scanner.Scan() {
		line := scanner.Text()
		if firstLine {
			firstLine = false
			if strings.TrimSpace(line) == "---" {
				inFrontMatter = true
				continue
			}
		}
		if inFrontMatter {
			if strings.TrimSpace(line) == "---" {
				inFrontMatter = false
			}
			continue
		}
		if _, err := fmt.Fprintln(output, line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	ok = true
	return nil
}
