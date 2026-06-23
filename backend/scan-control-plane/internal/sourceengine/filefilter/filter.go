package filefilter

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type ObjectInfo struct {
	DisplayName   string
	ObjectKey     string
	IsDocument    bool
	IsContainer   bool
	FileExtension string
	ProviderMeta  map[string]any
}

type Policy struct {
	includeExts     map[string]struct{}
	excludeExts     map[string]struct{}
	includePatterns []string
	excludePatterns []string
}

func FromBinding(binding store.Binding) Policy {
	p := Policy{
		includeExts: extensionsFromJSON(binding.IncludeExtensions),
		excludeExts: extensionsFromJSON(binding.ExcludeExtensions),
	}
	p.mergeProviderOptions(binding.ProviderOptions, len(p.includeExts) == 0)
	return p
}

func FromSourceBinding(source store.Source, binding store.Binding) Policy {
	p := Policy{
		includeExts: extensionsFromJSON(binding.IncludeExtensions),
		excludeExts: extensionsFromJSON(binding.ExcludeExtensions),
	}
	if len(p.includeExts) == 0 {
		p.includeExts = extensionsFromJSON(source.IncludeExtensions)
	}
	p.addExcludeExtensions(jsonStrings(source.ExcludeExtensions))
	p.mergeProviderOptions(binding.ProviderOptions, len(p.includeExts) == 0)
	return p
}

func FromProviderOptions(options map[string]any) Policy {
	p := Policy{}
	p.mergeProviderOptions(options, true)
	return p
}

func AllowsSourceObject(policy Policy, object store.SourceObject) bool {
	return policy.Allows(ObjectInfo{
		DisplayName:   object.DisplayName,
		ObjectKey:     object.ObjectKey,
		IsDocument:    object.IsDocument,
		IsContainer:   object.IsContainer,
		FileExtension: object.FileExtension,
		ProviderMeta:  object.ProviderMeta,
	})
}

func AllowsNormalized(policy Policy, object connector.NormalizedSourceObject) bool {
	return policy.Allows(ObjectInfo{
		DisplayName:   object.DisplayName,
		ObjectKey:     object.ObjectKey,
		IsDocument:    object.IsDocument,
		IsContainer:   object.IsContainer,
		FileExtension: object.FileExtension,
		ProviderMeta:  providerMetaMap(object.ProviderMeta),
	})
}

func (p Policy) Allows(object ObjectInfo) bool {
	if !object.IsDocument {
		return true
	}
	ext := EffectiveExtension(object)
	if _, excluded := p.excludeExts[ext]; ext != "" && excluded {
		return false
	}
	candidates := matchCandidates(object)
	if matchesAny(p.excludePatterns, candidates) {
		return false
	}
	if len(p.includeExts) == 0 && len(p.includePatterns) == 0 {
		return true
	}
	if ext != "" {
		if _, included := p.includeExts[ext]; included {
			return true
		}
	}
	return matchesAny(p.includePatterns, candidates)
}

func EffectiveExtension(object ObjectInfo) string {
	if feishuNativeMarkdown(object) {
		return ".md"
	}
	if ext := normalizeExtension(object.FileExtension); ext != "" {
		return ext
	}
	return normalizeExtension(filepath.Ext(strings.TrimSpace(object.DisplayName)))
}

func (p *Policy) mergeProviderOptions(options map[string]any, includeFallback bool) {
	if len(options) == 0 {
		return
	}
	if includeFallback {
		p.includePatterns = append(p.includePatterns, stringSlice(options["include_patterns"])...)
		p.addIncludeExtensions(stringSlice(options["include_extensions"]))
	}
	p.excludePatterns = append(p.excludePatterns, stringSlice(options["exclude_patterns"])...)
	p.addExcludeExtensions(stringSlice(options["exclude_extensions"]))
	if includeFallback {
		p.includeExts = addPatternExtensions(p.includeExts, p.includePatterns)
	}
	p.excludeExts = addPatternExtensions(p.excludeExts, p.excludePatterns)
}

func (p *Policy) addIncludeExtensions(values []string) {
	if len(values) == 0 {
		return
	}
	if p.includeExts == nil {
		p.includeExts = map[string]struct{}{}
	}
	for _, value := range values {
		if ext := normalizeExtension(value); ext != "" {
			p.includeExts[ext] = struct{}{}
		}
	}
}

func (p *Policy) addExcludeExtensions(values []string) {
	if len(values) == 0 {
		return
	}
	if p.excludeExts == nil {
		p.excludeExts = map[string]struct{}{}
	}
	for _, value := range values {
		if ext := normalizeExtension(value); ext != "" {
			p.excludeExts[ext] = struct{}{}
		}
	}
}

func addPatternExtensions(target map[string]struct{}, patterns []string) map[string]struct{} {
	if len(patterns) == 0 {
		return target
	}
	for _, pattern := range patterns {
		ext := normalizeExtension(path.Ext(strings.TrimSpace(pattern)))
		if ext == "" {
			continue
		}
		if target == nil {
			target = map[string]struct{}{}
		}
		target[ext] = struct{}{}
	}
	return target
}

func extensionsFromJSON(value store.JSON) map[string]struct{} {
	exts := map[string]struct{}{}
	for _, item := range jsonStrings(value) {
		if ext := normalizeExtension(item); ext != "" {
			exts[ext] = struct{}{}
		}
	}
	if len(exts) == 0 {
		return nil
	}
	return exts
}

func jsonStrings(value store.JSON) []string {
	return stringSlice(value["items"])
}

func normalizeExtension(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "." {
		return ""
	}
	value = strings.TrimPrefix(value, "*")
	if value == "" || value == "." {
		return ""
	}
	if !strings.HasPrefix(value, ".") {
		value = "." + value
	}
	if value == ".markdown" {
		return ".md"
	}
	return value
}

func feishuNativeMarkdown(object ObjectInfo) bool {
	kind := metaString(object.ProviderMeta, "kind")
	fileType := normalizedType(metaString(object.ProviderMeta, "file_type"))
	shortcutTargetType := normalizedType(metaString(object.ProviderMeta, "shortcut_target_type"))
	ext := normalizedType(object.FileExtension)
	switch kind {
	case "drive_file":
		return isFeishuDocType(fileType) || fileType == "shortcut" && isFeishuDocType(shortcutTargetType)
	case "wiki_node":
		return isFeishuDocType(fileType) || fileType == "" && (ext == "" || ext == "md" || isFeishuDocType(ext))
	default:
		return false
	}
}

func isFeishuDocType(value string) bool {
	switch normalizedType(value) {
	case "doc", "docx":
		return true
	default:
		return false
	}
}

func normalizedType(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func matchCandidates(object ObjectInfo) []string {
	candidates := []string{
		strings.TrimSpace(object.DisplayName),
		strings.TrimSpace(object.ObjectKey),
		metaString(object.ProviderMeta, "path"),
		metaString(object.ProviderMeta, "name"),
		metaString(object.ProviderMeta, "display_name"),
	}
	out := make([]string, 0, len(candidates)*2)
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = strings.Trim(strings.ReplaceAll(strings.TrimSpace(candidate), "\\", "/"), "/")
		if candidate == "" {
			continue
		}
		for _, value := range []string{candidate, path.Base(candidate)} {
			if _, ok := seen[value]; ok || value == "" || value == "." {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	if ext := EffectiveExtension(object); ext != "" {
		virtual := "object" + ext
		if _, ok := seen[virtual]; !ok {
			out = append(out, virtual)
		}
	}
	return out
}

func matchesAny(patterns []string, candidates []string) bool {
	for _, rawPattern := range patterns {
		pattern := strings.TrimSpace(rawPattern)
		if pattern == "" {
			continue
		}
		alt := ""
		if strings.HasPrefix(pattern, "**/") {
			alt = strings.TrimPrefix(pattern, "**/")
		}
		for _, candidate := range candidates {
			if matchesPattern(pattern, candidate) || alt != "" && matchesPattern(alt, candidate) {
				return true
			}
		}
	}
	return false
}

func matchesPattern(pattern, candidate string) bool {
	ok, err := path.Match(pattern, candidate)
	return err == nil && ok
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func metaString(meta map[string]any, key string) string {
	if len(meta) == 0 {
		return ""
	}
	if value, ok := meta[key].(string); ok {
		return value
	}
	return ""
}

func providerMetaMap(meta connector.ProviderMeta) map[string]any {
	if meta == nil {
		return nil
	}
	out := make(map[string]any, len(meta))
	for key, value := range meta {
		out[key] = value
	}
	return out
}
