package source

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	AdapterLawtextLaws    = "lawtext_laws"
	AdapterJustLaws       = "just_laws"
	AdapterGenAIBeginners = "genai_beginners"
)

func init() {
	Register(AdapterLawtextLaws, newLawtextAdapter)
	Register(AdapterJustLaws, newJustLawsAdapter)
	Register(AdapterGenAIBeginners, newGenAIBeginnersAdapter)
}

// lawtextLawsAdapter selects Chinese law Markdown files and names documents
// from their YAML front matter.
type lawtextLawsAdapter struct {
	titleField         string
	fallbackTitleField string
}

type lawtextFrontMatter struct {
	Title     string `yaml:"title"`
	LinkTitle string `yaml:"LinkTitle"`
	Status    string `yaml:"status"`
	Group     string `yaml:"group"`
}

func newLawtextAdapter(options Options) (Adapter, error) {
	return &lawtextLawsAdapter{
		titleField:         stringOption(options, "title_field", "LinkTitle"),
		fallbackTitleField: stringOption(options, "fallback_title_field", "title"),
	}, nil
}

func (a *lawtextLawsAdapter) ID() string { return AdapterLawtextLaws }

var lawtextInclude = compilePatterns(`^content/(法律|行政法规|司法解释|监察法规|宪法|appendix)/[^/]+\.md$`)

func (a *lawtextLawsAdapter) Match(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if !anyRegexpMatch(lawtextInclude, path) {
		return false
	}
	return filepath.Base(path) != "_index.md"
}

func (a *lawtextLawsAdapter) Materialize(ctx context.Context, root string, files []FileEntry) ([]IngestUnit, error) {
	units := make([]IngestUnit, 0, len(files))
	for _, file := range files {
		localPath := filepath.Join(root, filepath.FromSlash(file.Path))
		meta := readLawtextFrontMatter(localPath)
		title := frontMatterTitle(meta, a.titleField, a.titleField == "LinkTitle" && meta.LinkTitle != "")
		if title == "" {
			title = frontMatterTitle(meta, a.fallbackTitleField, false)
		}
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path))
		}
		group := strings.TrimSpace(meta.Group)
		if group == "" {
			group = firstPathSegment(file.Path, "content")
		}
		status := normalizeLawStatus(meta.Status)
		units = append(units, IngestUnit{
			LocalPath:    localPath,
			DisplayName:  sanitizePathPart(title) + ".md",
			RelativePath: filepath.ToSlash(filepath.Join(group, status)),
			Tags:         []string{"lawtext", group, status},
		})
	}
	return units, nil
}

// readLawtextFrontMatter extracts the initial YAML block without depending on
// a full static-site parser.
func readLawtextFrontMatter(path string) lawtextFrontMatter {
	var meta lawtextFrontMatter
	file, err := os.Open(path)
	if err != nil {
		return meta
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Scan()
	if strings.TrimSpace(scanner.Text()) != "---" {
		return meta
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
		return meta
	}
	_ = yaml.Unmarshal([]byte(strings.Join(block, "\n")), &meta)
	return meta
}

func frontMatterTitle(meta lawtextFrontMatter, field string, preferLinkTitle bool) string {
	if preferLinkTitle && strings.TrimSpace(meta.LinkTitle) != "" {
		return strings.TrimSpace(meta.LinkTitle)
	}
	switch field {
	case "LinkTitle":
		return strings.TrimSpace(meta.LinkTitle)
	case "title":
		return strings.TrimSpace(meta.Title)
	default:
		return strings.TrimSpace(meta.Title)
	}
}

func normalizeLawStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "有效", "生效":
		return "有效"
	case "已修改":
		return "已修改"
	case "已废止":
		return "已废止"
	case "尚未生效":
		return "尚未生效"
	default:
		if strings.TrimSpace(status) == "" {
			return "未知"
		}
		return strings.TrimSpace(status)
	}
}

func firstPathSegment(path, skip string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) > 0 && parts[0] == skip {
		parts = parts[1:]
	}
	if len(parts) > 0 {
		return parts[0]
	}
	return "其他"
}

// justLawsAdapter selects current-version law Markdown under docs/ and names
// documents from their first Markdown heading.
type justLawsAdapter struct{}

func newJustLawsAdapter(options Options) (Adapter, error) {
	return &justLawsAdapter{}, nil
}

func (a *justLawsAdapter) ID() string { return AdapterJustLaws }

var justLawsInclude = compilePatterns(`^docs/(administrative|economic|constitutional-relevance|civil-and-commercial|social|procedural|criminal-law|constitution|ecological-environment)/[^/]+/(README\.md|preamble\.md|[0-9]{2}-[^/]+\.md)$`)

func (a *justLawsAdapter) Match(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if strings.Contains(path, "/versions/") {
		return false
	}
	if !anyRegexpMatch(justLawsInclude, path) {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md"
}

func (a *justLawsAdapter) Materialize(ctx context.Context, root string, files []FileEntry) ([]IngestUnit, error) {
	units := make([]IngestUnit, 0, len(files))
	for _, file := range files {
		parts := strings.Split(filepath.ToSlash(file.Path), "/")
		category := parts[1]
		lawDir := parts[2]
		title := firstMarkdownHeading(filepath.Join(root, filepath.FromSlash(file.Path)))
		if title == "" {
			title = lawDir
		}
		units = append(units, IngestUnit{
			LocalPath:    filepath.Join(root, filepath.FromSlash(file.Path)),
			DisplayName:  sanitizePathPart(title) + ".md",
			RelativePath: filepath.ToSlash(category),
			Tags:         []string{"just_laws", category},
		})
	}
	return units, nil
}

func firstMarkdownHeading(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

// genAIBeginnersAdapter selects the original English course and the zh-CN
// translation while dropping code, notebooks, and images.
type genAIBeginnersAdapter struct {
	includeOriginalEnglish bool
}

func newGenAIBeginnersAdapter(options Options) (Adapter, error) {
	languages := stringSliceOption(options, "languages", []string{"zh-CN"})
	includeOriginalEnglish := boolOption(options, "include_original_en", false)
	for _, language := range languages {
		if language == "en-original" || language == "en" {
			includeOriginalEnglish = true
		}
	}
	return &genAIBeginnersAdapter{includeOriginalEnglish: includeOriginalEnglish}, nil
}

func (a *genAIBeginnersAdapter) ID() string { return AdapterGenAIBeginners }

var (
	genAIOriginalInclude = compilePatterns(
		`^(README|AGENTS)\.md$`,
		`^docs/[^/]+\.md$`,
		`^(0[0-9]|1[0-9]|2[0-1])-[^/]+/.*\.md$`,
	)
	genAIBinaryExclude = compilePatterns(
		`\.(webp|png|jpg|jpeg|gif|svg|ico)$`,
		`\.(ipynb|py|js|ts|json|dib|sh|bat|ps1)$`,
	)
)

func (a *genAIBeginnersAdapter) Match(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if anyRegexpMatch(genAIBinaryExclude, strings.ToLower(path)) {
		return false
	}
	if strings.HasPrefix(path, "translations/zh-CN/") && strings.HasSuffix(path, ".md") {
		return true
	}
	if a.includeOriginalEnglish && !strings.Contains(path, "/translations/") && anyRegexpMatch(genAIOriginalInclude, path) {
		return true
	}
	return false
}

func (a *genAIBeginnersAdapter) Materialize(ctx context.Context, root string, files []FileEntry) ([]IngestUnit, error) {
	units := make([]IngestUnit, 0, len(files))
	for _, file := range files {
		path := filepath.ToSlash(file.Path)
		display := path
		tags := []string{"generative-ai", "en-original"}
		if strings.HasPrefix(path, "translations/zh-CN/") {
			display = filepath.ToSlash(filepath.Join("zh-CN", strings.TrimPrefix(path, "translations/zh-CN/")))
			tags = []string{"generative-ai", "zh-CN"}
		}
		units = append(units, IngestUnit{
			LocalPath:    filepath.Join(root, filepath.FromSlash(file.Path)),
			DisplayName:  display,
			RelativePath: filepath.ToSlash(filepath.Dir(display)),
			Tags:         tags,
		})
	}
	return units, nil
}
