package localfs

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func validateTarget(targetType connector.TargetType, targetRef string) error {
	if targetType != TargetTypeLocalPath {
		return connector.NewError(connector.ErrorCodeInvalidTarget, "target_type is not supported")
	}
	if strings.TrimSpace(targetRef) == "" {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "target_ref is required")
	}
	return nil
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

func parseCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, connector.NewError(connector.ErrorCodeInvalidArgument, "cursor is invalid")
	}
	return offset, nil
}

func canonicalPath(info PathInfo) string {
	if info.NormalizedPath != "" {
		return cleanPath(info.NormalizedPath)
	}
	return cleanPath(info.Path)
}

func cleanPath(path string) string {
	return filepath.Clean(strings.TrimSpace(path))
}

func cleanPrefixes(prefixes []string) []string {
	out := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		if cleaned := cleanPath(prefix); cleaned != "." {
			out = append(out, cleaned)
		}
	}
	return out
}

func (c *LocalFSConnector) publicPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	cleaned := cleanPath(path)
	if c.publicRoot == "" || cleaned == "." {
		return cleaned
	}
	if c.isPublicRootPath(cleaned) {
		return cleaned
	}
	if cleaned == string(filepath.Separator) {
		return c.publicRoot
	}
	return filepath.Join(c.publicRoot, strings.TrimPrefix(cleaned, string(filepath.Separator)))
}

func (c *LocalFSConnector) virtualPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	cleaned := cleanPath(path)
	if c.publicRoot == "" || cleaned == "." || !c.isPublicRootPath(cleaned) {
		return cleaned
	}
	if cleaned == c.publicRoot {
		return string(filepath.Separator)
	}
	rel, err := filepath.Rel(c.publicRoot, cleaned)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return cleaned
	}
	return string(filepath.Separator) + rel
}

func (c *LocalFSConnector) isPublicRootPath(path string) bool {
	if c.publicRoot == "" {
		return false
	}
	return path == c.publicRoot || strings.HasPrefix(path, c.publicRoot+string(filepath.Separator))
}

func (c *LocalFSConnector) rejectOutsidePublicRoot(path string) error {
	if c.publicRoot == "" || c.isPublicRootPath(cleanPath(path)) {
		return nil
	}
	return connector.NewError(connector.ErrorCodePermissionDenied, "path is outside local_fs public root")
}

func (c *LocalFSConnector) pathAllowed(path string) bool {
	if len(c.allowedPrefixes) == 0 {
		return true
	}
	for _, prefix := range c.allowedPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func localTargetFingerprint(agentID, path string) string {
	return fmt.Sprintf("%s:%s:%s", ConnectorType, agentID, cleanPath(path))
}

func objectKeyFor(agentID string, info PathInfo) string {
	if info.StableID != "" {
		return stableObjectKey(agentID, info.StableID)
	}
	return pathObjectKey(agentID, canonicalPath(info))
}

func stableObjectKey(agentID, stableID string) string {
	return fmt.Sprintf("%s:%s:id:%s", ConnectorType, agentID, stableID)
}

func pathObjectKey(agentID, path string) string {
	return fmt.Sprintf("%s:%s:path:%s", ConnectorType, agentID, cleanPath(path))
}

func pathFromObjectKey(objectKey string) (string, bool) {
	needle := ":path:"
	index := strings.Index(objectKey, needle)
	if index < 0 {
		return "", false
	}
	return cleanPath(objectKey[index+len(needle):]), true
}

func parentRef(info PathInfo, fallback string) string {
	if info.ParentPath != "" {
		return cleanPath(info.ParentPath)
	}
	if fallback != "" {
		return cleanPath(fallback)
	}
	return ""
}

func parentKeyFor(agentID string, info PathInfo, fallback string) string {
	if info.ParentStableID != "" {
		return stableObjectKey(agentID, info.ParentStableID)
	}
	if parent := parentRef(info, fallback); parent != "" {
		return pathObjectKey(agentID, parent)
	}
	return ""
}

func displayName(name, path string) string {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	base := filepath.Base(cleanPath(path))
	if base == "." || base == string(filepath.Separator) {
		return cleanPath(path)
	}
	return base
}

func searchName(search, display string) string {
	if strings.TrimSpace(search) != "" {
		return strings.ToLower(strings.TrimSpace(search))
	}
	return strings.ToLower(strings.TrimSpace(display))
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

func dedupeRawObjects(items []connector.RawObject) []connector.RawObject {
	seen := make(map[string]struct{}, len(items))
	out := make([]connector.RawObject, 0, len(items))
	for _, item := range items {
		key := item.ObjectKey
		if key == "" {
			key = item.ObjectRef
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func dedupePathInfos(items []PathInfo) []PathInfo {
	seen := make(map[string]struct{}, len(items))
	out := make([]PathInfo, 0, len(items))
	for _, item := range items {
		path := canonicalPath(item)
		if path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, item)
	}
	return out
}

func sliceRawObjects(items []connector.RawObject, cursor string, pageSize int) (connector.RawObjectPage, error) {
	offset, err := parseCursor(cursor)
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	if offset >= len(items) {
		return connector.RawObjectPage{Items: []connector.RawObject{}}, nil
	}
	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}
	page := connector.RawObjectPage{Items: append([]connector.RawObject(nil), items[offset:end]...)}
	if end < len(items) {
		page.HasMore = true
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}
