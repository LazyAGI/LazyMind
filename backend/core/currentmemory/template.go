package currentmemory

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed templates/soul.yaml
var soulTemplateYAML []byte

//go:embed templates/profile.yaml
var profileTemplateYAML []byte

type MemoryDocument map[string]any

type SoulDocument = MemoryDocument
type ProfileDocument = MemoryDocument

type LocalizedText map[string]string

type PresentationField struct {
	Path        string        `json:"path" yaml:"path"`
	Labels      LocalizedText `json:"labels" yaml:"labels"`
	SummaryRole string        `json:"summary_role" yaml:"summary_role"`
}

type PresentationSection struct {
	Path   string              `json:"path" yaml:"path"`
	Labels LocalizedText       `json:"labels" yaml:"labels"`
	Fields []PresentationField `json:"fields" yaml:"fields"`
}

type MemoryPresentation struct {
	Fallbacks map[string]LocalizedText `json:"fallbacks" yaml:"fallbacks"`
	Sections  []PresentationSection    `json:"sections" yaml:"sections"`
}

type memoryTemplateDescriptor struct {
	SchemaVersion int                `yaml:"schema_version"`
	Document      MemoryDocument     `yaml:"document"`
	Presentation  MemoryPresentation `yaml:"presentation"`
}

type MemoryTemplate struct {
	Kind           string
	SchemaVersion  int
	Document       MemoryDocument
	Presentation   MemoryPresentation
	storedDefaults []byte
}

var (
	soulTemplate    = mustLoadMemoryTemplate("soul", soulTemplateYAML)
	profileTemplate = mustLoadMemoryTemplate("profile", profileTemplateYAML)

	CurrentSoulSchemaVersion    = soulTemplate.SchemaVersion
	CurrentProfileSchemaVersion = profileTemplate.SchemaVersion
	DefaultSoulYAML             = string(soulTemplate.storedDefaults)
	DefaultProfileYAML          = string(profileTemplate.storedDefaults)
)

func mustLoadMemoryTemplate(kind string, content []byte) *MemoryTemplate {
	var descriptor memoryTemplateDescriptor
	if err := yaml.Unmarshal(content, &descriptor); err != nil {
		panic(fmt.Sprintf("invalid %s memory template: %v", kind, err))
	}
	template := &MemoryTemplate{
		Kind:          kind,
		SchemaVersion: descriptor.SchemaVersion,
		Document:      normalizeYAMLValue(descriptor.Document).(MemoryDocument),
		Presentation:  descriptor.Presentation,
	}
	if err := template.validate(); err != nil {
		panic(fmt.Sprintf("invalid %s memory template: %v", kind, err))
	}
	stored, err := template.render(template.Document)
	if err != nil {
		panic(fmt.Sprintf("render %s memory template: %v", kind, err))
	}
	template.storedDefaults = stored
	return template
}

func templateForPath(entryPath string) *MemoryTemplate {
	switch strings.Trim(strings.TrimSpace(entryPath), "/") {
	case SoulPath:
		return soulTemplate
	case ProfilePath:
		return profileTemplate
	default:
		return nil
	}
}

func (t *MemoryTemplate) validate() error {
	if t.SchemaVersion < 1 {
		return fmt.Errorf("schema_version must be a positive integer")
	}
	if _, exists := t.Document["schema_version"]; exists {
		return fmt.Errorf("document must not contain the reserved version field")
	}
	fields := map[string]any{}
	if err := discoverTemplateFields(t.Document, "", fields); err != nil {
		return err
	}
	if len(fields) == 0 {
		return fmt.Errorf("document must contain at least one leaf field")
	}
	seen := map[string]bool{}
	titleCount := 0
	allowedRoles := map[string]bool{
		"title": true, "subtitle": true, "description": true, "tag": true, "none": true,
	}
	for _, section := range t.Presentation.Sections {
		if strings.TrimSpace(section.Path) == "" || !hasRequiredLabels(section.Labels) {
			return fmt.Errorf("presentation section requires path and labels")
		}
		sectionValue, exists := nestedValue(t.Document, section.Path)
		if !exists {
			return fmt.Errorf("presentation references unknown section %q", section.Path)
		}
		if _, isMapping := sectionValue.(MemoryDocument); !isMapping {
			return fmt.Errorf("presentation section %q must reference a mapping", section.Path)
		}
		for _, field := range section.Fields {
			if _, ok := fields[field.Path]; !ok {
				return fmt.Errorf("presentation references unknown field %q", field.Path)
			}
			if !strings.HasPrefix(field.Path, section.Path+".") {
				return fmt.Errorf(
					"presentation field %q is outside section %q",
					field.Path,
					section.Path,
				)
			}
			if seen[field.Path] {
				return fmt.Errorf("presentation field %q is duplicated", field.Path)
			}
			if !hasRequiredLabels(field.Labels) || !allowedRoles[field.SummaryRole] {
				return fmt.Errorf("presentation field %q has invalid metadata", field.Path)
			}
			seen[field.Path] = true
			if field.SummaryRole == "title" {
				titleCount++
			}
		}
	}
	if titleCount == 0 {
		return fmt.Errorf("presentation must declare at least one title field")
	}
	if len(seen) != len(fields) {
		missing := make([]string, 0, len(fields)-len(seen))
		for path := range fields {
			if !seen[path] {
				missing = append(missing, path)
			}
		}
		sort.Strings(missing)
		return fmt.Errorf("presentation is missing fields: %s", strings.Join(missing, ", "))
	}
	for _, role := range []string{"title", "subtitle", "description"} {
		labels := t.Presentation.Fallbacks[role]
		if strings.TrimSpace(labels["zh-CN"]) == "" || strings.TrimSpace(labels["en-US"]) == "" {
			return fmt.Errorf("presentation fallback %q requires zh-CN and en-US", role)
		}
	}
	return nil
}

func hasRequiredLabels(labels LocalizedText) bool {
	return strings.TrimSpace(labels["zh-CN"]) != "" &&
		strings.TrimSpace(labels["en-US"]) != ""
}

func discoverTemplateFields(node MemoryDocument, prefix string, fields map[string]any) error {
	for key, value := range node {
		if strings.TrimSpace(key) == "" || strings.Contains(key, ".") {
			return fmt.Errorf("mapping keys must be non-empty strings without dots")
		}
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		switch typed := value.(type) {
		case MemoryDocument:
			if err := discoverTemplateFields(typed, path, fields); err != nil {
				return err
			}
		case string, nil:
			fields[path] = typed
		case []any:
			if !isStringList(typed) {
				return fmt.Errorf("field %q must be a string, null, or list of strings", path)
			}
			fields[path] = typed
		default:
			return fmt.Errorf("field %q must be a string, null, or list of strings", path)
		}
	}
	return nil
}

func (t *MemoryTemplate) normalizeForRead(content []byte) (MemoryDocument, []byte) {
	root, version, ok := parseStoredMemoryDocument(content)
	if !ok || version < 0 || version > t.SchemaVersion {
		return cloneDocument(t.Document), append([]byte(nil), t.storedDefaults...)
	}
	if version == t.SchemaVersion {
		if err := t.validateDocument(root); err != nil {
			return cloneDocument(t.Document), append([]byte(nil), t.storedDefaults...)
		}
		return root, append([]byte(nil), content...)
	}
	reconciled := reconcileDocument(t.Document, root)
	if err := t.validateDocument(reconciled); err != nil {
		return cloneDocument(t.Document), append([]byte(nil), t.storedDefaults...)
	}
	rendered, err := t.render(reconciled)
	if err != nil {
		return cloneDocument(t.Document), append([]byte(nil), t.storedDefaults...)
	}
	return reconciled, rendered
}

func (t *MemoryTemplate) parseLatest(content []byte) (MemoryDocument, error) {
	document, version, ok := parseStoredMemoryDocument(content)
	if !ok || version != t.SchemaVersion {
		return nil, invalidDocument("%s schema_version must be %d", t.Kind, t.SchemaVersion)
	}
	if err := t.validateDocument(document); err != nil {
		return nil, err
	}
	return document, nil
}

func parseStoredMemoryDocument(content []byte) (MemoryDocument, int, bool) {
	var raw map[string]any
	if err := yaml.Unmarshal(content, &raw); err != nil || len(raw) == 0 {
		return nil, 0, false
	}
	normalized, ok := normalizeYAMLValue(raw).(MemoryDocument)
	if !ok {
		return nil, 0, false
	}
	version := 0
	if value, exists := normalized["schema_version"]; exists {
		parsed, valid := value.(int)
		if !valid || parsed < 1 {
			return nil, -1, true
		}
		version = parsed
		delete(normalized, "schema_version")
	}
	if len(normalized) == 0 {
		return nil, version, false
	}
	return normalized, version, true
}

func normalizeYAMLValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(MemoryDocument, len(typed))
		for key, item := range typed {
			result[key] = normalizeYAMLValue(item)
		}
		return result
	case map[any]any:
		result := make(MemoryDocument, len(typed))
		for key, item := range typed {
			text, ok := key.(string)
			if !ok {
				return typed
			}
			result[text] = normalizeYAMLValue(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = normalizeYAMLValue(item)
		}
		return result
	default:
		return value
	}
}

func (t *MemoryTemplate) validateDocument(document MemoryDocument) error {
	return validateAgainstTemplate(t.Document, document, t.Kind)
}

func validateAgainstTemplate(template, document MemoryDocument, prefix string) error {
	if len(template) != len(document) {
		return invalidDocument("%s fields do not match the latest template", prefix)
	}
	for key, expected := range template {
		actual, ok := document[key]
		if !ok {
			return invalidDocument("%s is missing field %q", prefix, key)
		}
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		expectedMapping, expectsMapping := expected.(MemoryDocument)
		actualMapping, actualIsMapping := actual.(MemoryDocument)
		if expectsMapping {
			if !actualIsMapping {
				return invalidDocument("field %q must be a mapping", path)
			}
			if err := validateAgainstTemplate(expectedMapping, actualMapping, path); err != nil {
				return err
			}
			continue
		}
		if actualIsMapping {
			return invalidDocument("field %q must be a leaf value", path)
		}
		switch expected.(type) {
		case string:
			if _, ok := actual.(string); !ok {
				return invalidDocument("field %q must be a string", path)
			}
		case nil:
			if actual != nil {
				if _, ok := actual.(string); !ok {
					return invalidDocument("field %q must be a string or null", path)
				}
			}
		case []any:
			values, ok := actual.([]any)
			if !ok || !isStringList(values) {
				return invalidDocument("field %q must be a list of strings", path)
			}
		}
	}
	return nil
}

func reconcileDocument(template, old MemoryDocument) MemoryDocument {
	result := cloneDocument(template)
	for key, expected := range template {
		actual, exists := old[key]
		if !exists {
			continue
		}
		expectedMapping, expectsMapping := expected.(MemoryDocument)
		actualMapping, actualIsMapping := actual.(MemoryDocument)
		if expectsMapping {
			if actualIsMapping {
				result[key] = reconcileDocument(expectedMapping, actualMapping)
			}
			continue
		}
		if converted, ok := reconcileLeaf(expected, actual); ok {
			result[key] = converted
		}
	}
	return result
}

func reconcileLeaf(expected, actual any) (any, bool) {
	switch expected.(type) {
	case string:
		switch typed := actual.(type) {
		case string:
			return typed, true
		case []any:
			if isStringList(typed) {
				return joinStringList(typed), true
			}
		}
	case nil:
		switch typed := actual.(type) {
		case nil, string:
			return typed, true
		case []any:
			if isStringList(typed) {
				return joinStringList(typed), true
			}
		}
	case []any:
		switch typed := actual.(type) {
		case string:
			if strings.TrimSpace(typed) == "" {
				return []any{}, true
			}
			return []any{typed}, true
		case []any:
			if isStringList(typed) {
				return cloneValue(typed), true
			}
		}
	}
	return nil, false
}

func joinStringList(values []any) string {
	items := make([]string, len(values))
	for index, value := range values {
		items[index] = value.(string)
	}
	return strings.Join(items, "， ")
}

func isStringList(values []any) bool {
	for _, value := range values {
		if _, ok := value.(string); !ok {
			return false
		}
	}
	return true
}

func cloneDocument(document MemoryDocument) MemoryDocument {
	return cloneValue(document).(MemoryDocument)
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case MemoryDocument:
		result := make(MemoryDocument, len(typed))
		for key, item := range typed {
			result[key] = cloneValue(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneValue(item)
		}
		return result
	default:
		return value
	}
}

func (t *MemoryTemplate) render(document MemoryDocument) ([]byte, error) {
	if err := t.validateDocument(document); err != nil {
		return nil, err
	}
	stored := struct {
		SchemaVersion int            `yaml:"schema_version"`
		Document      MemoryDocument `yaml:",inline"`
	}{
		SchemaVersion: t.SchemaVersion,
		Document:      cloneDocument(document),
	}
	return yaml.Marshal(stored)
}

func nestedValue(document MemoryDocument, path string) (any, bool) {
	var current any = document
	for _, part := range strings.Split(path, ".") {
		var mapping map[string]any
		switch typed := current.(type) {
		case MemoryDocument:
			mapping = typed
		case map[string]any:
			mapping = typed
		default:
			return nil, false
		}
		var ok bool
		current, ok = mapping[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func setNestedValue(document MemoryDocument, path string, value any) bool {
	parts := strings.Split(path, ".")
	var current MemoryDocument = document
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(MemoryDocument)
		if !ok {
			return false
		}
		current = next
	}
	leaf := parts[len(parts)-1]
	if _, exists := current[leaf]; !exists {
		return false
	}
	if _, isMapping := current[leaf].(MemoryDocument); isMapping {
		return false
	}
	current[leaf] = value
	return true
}
