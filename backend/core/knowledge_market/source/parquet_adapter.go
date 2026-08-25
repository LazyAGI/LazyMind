package source

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/parquet-go/parquet-go"
)

const AdapterChinataxPolicy = "chinatax_policy"

func init() {
	Register(AdapterChinataxPolicy, newChinataxPolicyAdapter)
}

// chinataxPolicyAdapter turns the Hugging Face chinatax-policy-corpus Parquet
// shard into one Markdown document per tax policy row.
type chinataxPolicyAdapter struct{}

func newChinataxPolicyAdapter(options Options) (Adapter, error) {
	return &chinataxPolicyAdapter{}, nil
}

func (a *chinataxPolicyAdapter) ID() string { return AdapterChinataxPolicy }

func (a *chinataxPolicyAdapter) Match(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || strings.HasPrefix(path, ".") {
		return false
	}
	return strings.EqualFold(filepath.Ext(path), ".parquet")
}

func (a *chinataxPolicyAdapter) Materialize(ctx context.Context, root string, files []FileEntry) ([]IngestUnit, error) {
	_ = ctx
	generatedDir := filepath.Join(root, ".ingest_chinatax")
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		return nil, fmt.Errorf("create chinatax generated dir: %w", err)
	}

	units := make([]IngestUnit, 0)
	seq := 0
	for _, file := range files {
		if !a.Match(file.Path) {
			continue
		}
		rows, err := readChinataxRows(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			return nil, fmt.Errorf("read chinatax parquet %s: %w", file.Path, err)
		}
		for _, row := range rows {
			seq++
			title := nonEmptyString(rowString(row["title"]), rowString(row["document_number"]), fmt.Sprintf("税务政策_%d", seq))
			displayName := sanitizePathPart(title) + ".md"
			localPath := filepath.Join(generatedDir, fmt.Sprintf("%05d_%s.md", seq, sanitizePathPart(title)))
			effectLevel := strings.TrimSpace(rowString(row["effect_level"]))
			relativePath := filepath.ToSlash(filepath.Join("税务政策", firstNonEmpty(sanitizePathPart(effectLevel), "其他")))
			tags := normalizeTags([]string{"chinatax", effectLevel, rowString(row["tax_type"])})
			content := buildChinataxPolicyDocument(title, row)
			if err := os.WriteFile(localPath, []byte(content), 0o644); err != nil {
				return nil, fmt.Errorf("write chinatax document %s: %w", localPath, err)
			}
			units = append(units, IngestUnit{
				LocalPath:    localPath,
				DisplayName:  displayName,
				RelativePath: relativePath,
				Tags:         tags,
			})
		}
	}
	return units, nil
}

// readChinataxRows reads every row from a Parquet shard as a generic map so the
// adapter does not depend on the dataset's exact Parquet schema.
func readChinataxRows(path string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	pf, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		return nil, err
	}
	reader := parquet.NewReader(pf)
	defer reader.Close()

	rows := make([]map[string]any, 0)
	for {
		row := map[string]any{}
		err := reader.Read(&row)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// buildChinataxPolicyDocument renders one policy row into Markdown with the
// structured fields preserved as human-readable metadata.
func buildChinataxPolicyDocument(title string, row map[string]any) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")

	fields := [][2]string{
		{"文号", "document_number"},
		{"效力级别", "effect_level"},
		{"税种", "tax_type"},
		{"时效性", "aging"},
		{"发布机构", "issuing_department"},
		{"成文日期", "written_date"},
		{"来源", "url"},
	}
	for _, field := range fields {
		value := strings.TrimSpace(rowString(row[field[1]]))
		if value == "" {
			continue
		}
		b.WriteString("**")
		b.WriteString(field[0])
		b.WriteString("：** ")
		b.WriteString(value)
		b.WriteString("\n\n")
	}

	content := strings.TrimSpace(rowString(row["content"]))
	if content == "" {
		content = strings.TrimSpace(rowString(row["title"]))
	}
	if content != "" {
		b.WriteString(content)
		b.WriteString("\n")
	}
	return b.String()
}

func rowString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}

func nonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	return nonEmptyString(values...)
}

func normalizeTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}
