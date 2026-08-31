package source

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// genericGroup is the common group rule shared by generic adapters. Groups let
// one YAML source describe multiple path-specific naming/tagging rules.
type genericGroup struct {
	Name                 string   `yaml:"name"`
	Match                []string `yaml:"match"`
	StripPrefix          string   `yaml:"strip_prefix"`
	DisplayNameTemplate  string   `yaml:"display_name_template"`
	RelativePathTemplate string   `yaml:"relative_path_template"`
	Tags                 []string `yaml:"tags"`
}

// compiledGroup is a genericGroup whose match patterns have been compiled.
type compiledGroup struct {
	genericGroup
	match []*regexp.Regexp
}

// genericFileContext contains the template fields available for one selected
// package file. Segments are zero-based path components such as seg0, seg1.
type genericFileContext struct {
	Path      string
	Dir       string
	Base      string
	Name      string
	Ext       string
	Size      string
	SHA256    string
	GroupName string
	Segments  []string
}

// decodeStrictOptions converts the free-form Options map into a typed config
// and rejects unknown keys. Validation happens when the adapter is created
// during an install job, so invalid YAML fails the install rather than startup.
func decodeStrictOptions(options Options, target any, adapterID string) error {
	if options == nil {
		options = Options{}
	}
	raw, err := yaml.Marshal(options)
	if err != nil {
		return fmt.Errorf("adapter %s: marshal source_options: %w", adapterID, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("adapter %s: invalid source_options: %w", adapterID, err)
	}
	return nil
}

func compilePatternList(patterns []string, field string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			return nil, fmt.Errorf("%s contains an empty pattern", field)
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("%s pattern %q is invalid: %w", field, pattern, err)
		}
		out = append(out, compiled)
	}
	return out, nil
}

func validateExtensions(extensions []string) error {
	for _, ext := range extensions {
		ext = strings.TrimSpace(ext)
		if ext == "" || !strings.HasPrefix(ext, ".") || len(ext) == 1 {
			return fmt.Errorf("extension %q must look like .ext", ext)
		}
	}
	return nil
}

func validateTemplateSyntax(template string) error {
	depth := 0
	for i := 0; i < len(template); {
		switch {
		case strings.HasPrefix(template[i:], "{{"):
			depth++
			if depth > 1 {
				return fmt.Errorf("nested template expressions are not supported")
			}
			i += 2
		case strings.HasPrefix(template[i:], "}}"):
			depth--
			if depth < 0 {
				return fmt.Errorf("unexpected closing braces in template")
			}
			i += 2
		default:
			i++
		}
	}
	if depth != 0 {
		return fmt.Errorf("unclosed template expression in template")
	}
	return nil
}

func renderTemplate(template string, values map[string]string, allowMissing bool) (string, error) {
	if err := validateTemplateSyntax(template); err != nil {
		return "", err
	}
	var out strings.Builder
	for {
		open := strings.Index(template, "{{")
		if open < 0 {
			out.WriteString(template)
			break
		}
		out.WriteString(template[:open])
		close := strings.Index(template[open+2:], "}}")
		if close < 0 {
			return "", fmt.Errorf("unclosed template expression")
		}
		key := strings.TrimSpace(template[open+2 : open+2+close])
		if key == "" {
			return "", fmt.Errorf("empty template expression")
		}
		value, ok := values[key]
		if !ok {
			if allowMissing {
				value = ""
			} else {
				return "", fmt.Errorf("unknown template field %q", key)
			}
		}
		out.WriteString(value)
		template = template[open+2+close+2:]
	}
	return out.String(), nil
}

func renderTags(tags []string, values map[string]string, allowMissing bool) ([]string, error) {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		rendered, err := renderTemplate(tag, values, allowMissing)
		if err != nil {
			return nil, err
		}
		out = append(out, rendered)
	}
	return normalizeTags(out), nil
}

func newGenericFileContext(file FileEntry, stripPrefix, groupName string) genericFileContext {
	filePath := strings.ReplaceAll(strings.TrimSpace(file.Path), "\\", "/")
	filePath = strings.Trim(filePath, "/")
	if stripPrefix = strings.TrimSpace(stripPrefix); stripPrefix != "" && strings.HasPrefix(filePath, stripPrefix) {
		filePath = strings.TrimPrefix(filePath, stripPrefix)
	}
	filePath = strings.Trim(filePath, "/")
	base := path.Base(filePath)
	ext := strings.ToLower(path.Ext(base))
	segments := []string{}
	if filePath != "" {
		segments = strings.Split(filePath, "/")
	}
	dir := path.Dir(filePath)
	if dir == "." || dir == "/" {
		dir = ""
	}
	return genericFileContext{
		Path:      filePath,
		Dir:       dir,
		Base:      base,
		Name:      strings.TrimSuffix(base, ext),
		Ext:       ext,
		Size:      strconv.FormatInt(file.Size, 10),
		SHA256:    file.SHA256,
		GroupName: groupName,
		Segments:  segments,
	}
}

func (c genericFileContext) values() map[string]string {
	values := map[string]string{
		"path":       c.Path,
		"dir":        c.Dir,
		"basename":   c.Base,
		"name":       c.Name,
		"ext":        c.Ext,
		"size":       c.Size,
		"sha256":     c.SHA256,
		"group_name": c.GroupName,
	}
	for index, segment := range c.Segments {
		values[fmt.Sprintf("seg%d", index)] = segment
	}
	return values
}

func cleanDisplayName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.ReplaceAll(value, "..", "")
	value = strings.Trim(value, "/")
	if value == "" {
		return "document"
	}
	return value
}

func cleanRelativePath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.ReplaceAll(value, "..", "")
	return strings.Trim(value, "/")
}

func compileGenericGroups(groups []genericGroup) ([]compiledGroup, error) {
	out := make([]compiledGroup, 0, len(groups))
	seen := map[string]struct{}{}
	for _, group := range groups {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			return nil, fmt.Errorf("generic group name is required")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("duplicate generic group name %q", name)
		}
		seen[name] = struct{}{}
		if len(group.Match) == 0 {
			return nil, fmt.Errorf("generic group %q requires at least one match pattern", name)
		}
		patterns, err := compilePatternList(group.Match, "groups."+name+".match")
		if err != nil {
			return nil, err
		}
		if err := validateTemplateSyntax(group.DisplayNameTemplate); err != nil {
			return nil, fmt.Errorf("generic group %q display_name_template: %w", name, err)
		}
		if err := validateTemplateSyntax(group.RelativePathTemplate); err != nil {
			return nil, fmt.Errorf("generic group %q relative_path_template: %w", name, err)
		}
		for _, tag := range group.Tags {
			if err := validateTemplateSyntax(tag); err != nil {
				return nil, fmt.Errorf("generic group %q tag template: %w", name, err)
			}
		}
		group.Name = name
		group.StripPrefix = strings.TrimSpace(group.StripPrefix)
		out = append(out, compiledGroup{genericGroup: group, match: patterns})
	}
	return out, nil
}

func matchesAny(patterns []*regexp.Regexp, value string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

// genericMatchRule implements extension, include, exclude and group matching
// shared by the three generic adapters.
type genericMatchRule struct {
	extensions map[string]struct{}
	include    []*regexp.Regexp
	exclude    []*regexp.Regexp
	groups     []compiledGroup
}

func newGenericMatchRule(include, exclude, extensions []string, groups []genericGroup) (*genericMatchRule, error) {
	if err := validateExtensions(extensions); err != nil {
		return nil, err
	}
	includeRegex, err := compilePatternList(include, "include")
	if err != nil {
		return nil, err
	}
	excludeRegex, err := compilePatternList(exclude, "exclude")
	if err != nil {
		return nil, err
	}
	compiledGroups, err := compileGenericGroups(groups)
	if err != nil {
		return nil, err
	}
	rule := &genericMatchRule{
		extensions: map[string]struct{}{},
		include:    includeRegex,
		exclude:    excludeRegex,
		groups:     compiledGroups,
	}
	for _, ext := range extensions {
		rule.extensions[strings.ToLower(strings.TrimSpace(ext))] = struct{}{}
	}
	return rule, nil
}

func normalizePackagePath(filePath string) string {
	return strings.ReplaceAll(strings.TrimSpace(filePath), "\\", "/")
}

func (r *genericMatchRule) matches(filePath string) bool {
	filePath = normalizePackagePath(filePath)
	if len(r.extensions) > 0 {
		if _, ok := r.extensions[strings.ToLower(path.Ext(filePath))]; !ok {
			return false
		}
	}
	if len(r.include) > 0 && !matchesAny(r.include, filePath) {
		return false
	}
	if matchesAny(r.exclude, filePath) {
		return false
	}
	if len(r.groups) == 0 {
		return true
	}
	for index := range r.groups {
		if matchesAny(r.groups[index].match, filePath) {
			return true
		}
	}
	return false
}

func (r *genericMatchRule) resolveGroup(filePath string) (compiledGroup, bool) {
	filePath = normalizePackagePath(filePath)
	for _, group := range r.groups {
		if matchesAny(group.match, filePath) {
			return group, true
		}
	}
	return compiledGroup{}, false
}
