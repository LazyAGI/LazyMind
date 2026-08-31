package source

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/parquet-go/parquet-go"
)

const AdapterGenericTabular = "generic_tabular"

var errTabularRowLimit = errors.New("tabular row limit reached")

type genericTabularConfig struct {
	InputExtensions       []string          `yaml:"input_extensions"`
	Include               []string          `yaml:"include"`
	Exclude               []string          `yaml:"exclude"`
	DocMode               string            `yaml:"doc_mode"`
	TitleFields           []string          `yaml:"title_fields"`
	TitleFallbackTemplate string            `yaml:"title_fallback_template"`
	FieldDefaults         map[string]string `yaml:"field_defaults"`
	DocumentTemplate      string            `yaml:"document_template"`
	DisplayNameTemplate   string            `yaml:"display_name_template"`
	RelativePathTemplate  string            `yaml:"relative_path_template"`
	Tags                  []string          `yaml:"tags"`
	RowLimit              int               `yaml:"row_limit"`
	Groups                []genericGroup    `yaml:"groups"`
}

type genericTabularAdapter struct {
	rule                  *genericMatchRule
	titleFields           []string
	titleFallbackTemplate string
	fieldDefaults         map[string]string
	documentTemplate      string
	displayNameTemplate   string
	relativePathTemplate  string
	tags                  []string
	rowLimit              int
}

func init() {
	Register(AdapterGenericTabular, newGenericTabularAdapter)
}

func newGenericTabularAdapter(options Options) (Adapter, error) {
	var config genericTabularConfig
	if err := decodeStrictOptions(options, &config, AdapterGenericTabular); err != nil {
		return nil, err
	}
	if len(config.InputExtensions) == 0 {
		return nil, fmt.Errorf("input_extensions is required")
	}
	config.DocMode = strings.ToLower(strings.TrimSpace(config.DocMode))
	if config.DocMode == "" {
		config.DocMode = "row"
	}
	if config.DocMode != "row" {
		return nil, fmt.Errorf("doc_mode %q is not supported; only row is currently supported", config.DocMode)
	}
	config.DocumentTemplate = strings.TrimSpace(config.DocumentTemplate)
	if config.DocumentTemplate == "" {
		return nil, fmt.Errorf("document_template is required for doc_mode=row")
	}
	config.DisplayNameTemplate = strings.TrimSpace(config.DisplayNameTemplate)
	config.RelativePathTemplate = strings.TrimSpace(config.RelativePathTemplate)
	config.TitleFallbackTemplate = strings.TrimSpace(config.TitleFallbackTemplate)
	if config.DisplayNameTemplate == "" {
		config.DisplayNameTemplate = "{{title}}.md"
	}
	if config.RelativePathTemplate == "" {
		config.RelativePathTemplate = "{{dir}}"
	}
	if config.TitleFallbackTemplate == "" {
		config.TitleFallbackTemplate = "document_{{seq}}"
	}
	if config.RowLimit < 0 {
		return nil, fmt.Errorf("row_limit must be zero or a positive number")
	}
	for name, template := range map[string]string{
		"document_template":       config.DocumentTemplate,
		"display_name_template":   config.DisplayNameTemplate,
		"relative_path_template":  config.RelativePathTemplate,
		"title_fallback_template": config.TitleFallbackTemplate,
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
	rule, err := newGenericMatchRule(config.Include, config.Exclude, config.InputExtensions, config.Groups)
	if err != nil {
		return nil, err
	}
	return &genericTabularAdapter{
		rule:                  rule,
		titleFields:           config.TitleFields,
		titleFallbackTemplate: config.TitleFallbackTemplate,
		fieldDefaults:         config.FieldDefaults,
		documentTemplate:      config.DocumentTemplate,
		displayNameTemplate:   config.DisplayNameTemplate,
		relativePathTemplate:  config.RelativePathTemplate,
		tags:                  config.Tags,
		rowLimit:              config.RowLimit,
	}, nil
}

func (a *genericTabularAdapter) ID() string { return AdapterGenericTabular }

func (a *genericTabularAdapter) Match(path string) bool {
	return a.rule.matches(path)
}

func (a *genericTabularAdapter) Materialize(ctx context.Context, root string, files []FileEntry) ([]IngestUnit, error) {
	generatedDir := filepath.Join(root, ".ingest_generic_tabular")
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		return nil, fmt.Errorf("create generic tabular generated dir: %w", err)
	}
	units := make([]IngestUnit, 0)
	seq := 0
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
		localInput := filepath.Join(root, filepath.FromSlash(file.Path))
		err := forEachTabularRow(ctx, localInput, file.Path, func(row map[string]any) error {
			if a.rowLimit > 0 && seq >= a.rowLimit {
				return errTabularRowLimit
			}
			seq++
			rowValues := tabularRowStrings(row, a.fieldDefaults)
			fileContext := newGenericFileContext(file, group.StripPrefix, groupName)
			values := fileContext.values()
			values["seq"] = strconv.Itoa(seq)
			for key, value := range rowValues {
				values[key] = value
			}

			title := resolveTabularTitle(rowValues, a.titleFields, a.titleFallbackTemplate, values, seq)
			values["title"] = title

			content, err := renderTemplate(a.documentTemplate, values, true)
			if err != nil {
				return fmt.Errorf("render document template for %s row %d: %w", file.Path, seq, err)
			}
			displayName, err := renderTemplate(displayTemplate, values, true)
			if err != nil {
				return fmt.Errorf("render display name for %s row %d: %w", file.Path, seq, err)
			}
			displayName = ensureMarkdownDisplayName(cleanDisplayName(displayName))
			relativePath, err := renderTemplate(relativeTemplate, values, true)
			if err != nil {
				return fmt.Errorf("render relative path for %s row %d: %w", file.Path, seq, err)
			}
			relativePath = cleanRelativePath(relativePath)
			renderedTags, err := renderTags(tags, values, true)
			if err != nil {
				return fmt.Errorf("render tags for %s row %d: %w", file.Path, seq, err)
			}

			localPath := filepath.Join(generatedDir, generatedTabularFileName(seq, displayName))
			if err := os.WriteFile(localPath, []byte(content), 0o644); err != nil {
				return fmt.Errorf("write tabular document %s: %w", localPath, err)
			}
			units = append(units, IngestUnit{
				LocalPath:    localPath,
				DisplayName:  displayName,
				RelativePath: relativePath,
				Tags:         renderedTags,
			})
			return nil
		})
		if errors.Is(err, errTabularRowLimit) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tabular rows from %s: %w", file.Path, err)
		}
	}
	return units, nil
}

func tabularRowStrings(row map[string]any, defaults map[string]string) map[string]string {
	out := make(map[string]string, len(row)+len(defaults))
	for key, value := range row {
		out[key] = strings.TrimSpace(rowString(value))
	}
	for key, fallback := range defaults {
		if strings.TrimSpace(out[key]) == "" {
			out[key] = strings.TrimSpace(fallback)
		}
	}
	return out
}

func resolveTabularTitle(row map[string]string, titleFields []string, fallbackTemplate string, values map[string]string, seq int) string {
	for _, field := range titleFields {
		if value := strings.TrimSpace(row[strings.TrimSpace(field)]); value != "" {
			return value
		}
	}
	if fallbackTemplate != "" {
		if value, err := renderTemplate(fallbackTemplate, values, true); err == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return fmt.Sprintf("document_%d", seq)
}

func ensureMarkdownDisplayName(name string) string {
	if path.Ext(name) == "" {
		return name + ".md"
	}
	return name
}

func generatedTabularFileName(seq int, displayName string) string {
	ext := path.Ext(displayName)
	base := strings.TrimSuffix(displayName, ext)
	base = sanitizePathPart(base)
	return fmt.Sprintf("%08d_%s%s", seq, base, ext)
}

func forEachTabularRow(ctx context.Context, localPath, filePath string, fn func(map[string]any) error) error {
	switch strings.ToLower(path.Ext(filePath)) {
	case ".csv":
		return forEachCSVRow(ctx, localPath, ',', fn)
	case ".tsv":
		return forEachCSVRow(ctx, localPath, '\t', fn)
	case ".parquet":
		return forEachParquetRow(ctx, localPath, fn)
	case ".jsonl", ".ndjson":
		return forEachJSONLRow(ctx, localPath, fn)
	default:
		return fmt.Errorf("unsupported tabular extension %q", path.Ext(filePath))
	}
}

func forEachCSVRow(ctx context.Context, localPath string, comma rune, fn func(map[string]any) error) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = comma
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	header, err := reader.Read()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		record, err := reader.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		row := make(map[string]any, len(header))
		for index, value := range record {
			if index >= len(header) {
				break
			}
			key := strings.TrimSpace(header[index])
			if key == "" {
				continue
			}
			row[key] = value
		}
		if err := fn(row); err != nil {
			return err
		}
	}
}

func forEachParquetRow(ctx context.Context, localPath string, fn func(map[string]any) error) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	parquetFile, err := parquet.OpenFile(file, info.Size())
	if err != nil {
		return err
	}
	reader := parquet.NewReader(parquetFile)
	defer reader.Close()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		row := map[string]any{}
		err := reader.Read(&row)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(row); err != nil {
			return err
		}
	}
}

func forEachJSONLRow(ctx context.Context, localPath string, fn func(map[string]any) error) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		row := map[string]any{}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return fmt.Errorf("decode JSONL row: %w", err)
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	return scanner.Err()
}
