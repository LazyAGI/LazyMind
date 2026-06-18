package notion

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector/feishu"
)

const defaultPageSize = 100

var notionIDPattern = regexp.MustCompile(`(?i)([0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)

func tokenRequest(authConnectionID, userID string) feishu.TokenRequest {
	return feishu.TokenRequest{AuthConnectionID: strings.TrimSpace(authConnectionID), UserID: strings.TrimSpace(userID)}
}

func normalizeNotionID(raw string) string {
	raw = strings.Trim(strings.TrimSpace(raw), "<>")
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil {
		resolved := false
		for _, key := range []string{"page_id", "database_id", "block_id", "p"} {
			if value := strings.TrimSpace(parsed.Query().Get(key)); value != "" {
				raw = value
				resolved = true
				break
			}
		}
		if !resolved && parsed.Host != "" && parsed.Path != "" {
			parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
			if len(parts) > 0 {
				raw = parts[len(parts)-1]
			}
		}
	}
	raw = strings.TrimSuffix(raw, "?")
	matches := notionIDPattern.FindAllString(raw, -1)
	if len(matches) == 0 {
		return strings.ReplaceAll(raw, "-", "")
	}
	return strings.ReplaceAll(matches[len(matches)-1], "-", "")
}

func validatePageSize(pageSize, maxPageSize int) error {
	if pageSize <= 0 {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "page_size must be positive")
	}
	if pageSize > maxPageSize {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "page_size exceeds connector max_page_size")
	}
	return nil
}

func pageSize(requested, max int) int {
	if requested <= 0 {
		requested = defaultPageSize
	}
	if max > 0 && requested > max {
		return max
	}
	return requested
}

func displayName(name, fallback string) string {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return strings.TrimSpace(fallback)
}

func searchName(search, display string) string {
	if strings.TrimSpace(search) != "" {
		return strings.ToLower(strings.TrimSpace(search))
	}
	return strings.ToLower(strings.TrimSpace(display))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneProviderMeta(meta connector.ProviderMeta) connector.ProviderMeta {
	if meta == nil {
		return nil
	}
	cloned := make(connector.ProviderMeta, len(meta))
	for key, value := range meta {
		cloned[key] = value
	}
	return cloned
}

func valueAsString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func boolOption(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, ok := m[key].(bool)
	return ok && v
}

func parseNotionTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		utc := t.UTC()
		return &utc
	}
	return nil
}

func markdownHeading(level int) string {
	if level < 1 {
		level = 1
	}
	if level > 6 {
		level = 6
	}
	return strings.Repeat("#", level)
}

func joinMarkdown(lines []string) string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n\n")
}

func richText(raw any) string {
	items, ok := raw.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		obj, _ := item.(map[string]any)
		text := strings.TrimSpace(valueAsString(obj["plain_text"]))
		if text == "" {
			textObj, _ := obj["text"].(map[string]any)
			text = strings.TrimSpace(valueAsString(textObj["content"]))
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "")
}

func pageTitle(pageObj map[string]any) string {
	props, _ := pageObj["properties"].(map[string]any)
	for _, value := range props {
		prop, _ := value.(map[string]any)
		if strings.TrimSpace(valueAsString(prop["type"])) == "title" {
			return richText(prop["title"])
		}
	}
	if title := richText(pageObj["title"]); title != "" {
		return title
	}
	return ""
}

func databaseTitle(dbObj map[string]any) string {
	return richText(dbObj["title"])
}

func fileBlockURL(content map[string]any) string {
	for _, key := range []string{"external", "file"} {
		obj, _ := content[key].(map[string]any)
		if value := strings.TrimSpace(valueAsString(obj["url"])); value != "" {
			return value
		}
	}
	return ""
}
