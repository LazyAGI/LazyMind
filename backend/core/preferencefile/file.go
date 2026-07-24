package preferencefile

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"lazymind/core/common/orm"
)

type PreferenceFile struct {
	AgentPersona   string
	PreferredName  string
	ResponseStyle  string
	Content        string
	Raw            string
	meta           *yaml.Node
	hasFrontmatter bool
}

type PreferencePatch struct {
	Content       *string
	AgentPersona  *string
	PreferredName *string
	ResponseStyle *string
}

func BuildInitialFileContent(row orm.SystemUserPreference) string {
	file := PreferenceFile{
		AgentPersona:   row.AgentPersona,
		PreferredName:  row.PreferredName,
		ResponseStyle:  row.ResponseStyle,
		Content:        row.Content,
		meta:           emptyPreferenceMetaNode(),
		hasFrontmatter: true,
	}
	setYAMLString(file.meta, "agent_persona", row.AgentPersona)
	setYAMLString(file.meta, "preferred_name", row.PreferredName)
	setYAMLString(file.meta, "response_style", row.ResponseStyle)
	return serializePreferenceFile(file)
}

func EmptyPreferenceFileContent() string {
	return BuildInitialFileContent(orm.SystemUserPreference{})
}

func ParseFileContent(content string) (PreferenceFile, error) {
	meta, body, hasFrontmatter, err := splitPreferenceFrontmatter(content)
	if err != nil {
		return PreferenceFile{}, err
	}
	file := PreferenceFile{
		AgentPersona:   yamlString(meta, "agent_persona"),
		PreferredName:  yamlString(meta, "preferred_name"),
		ResponseStyle:  yamlString(meta, "response_style"),
		Content:        body,
		Raw:            content,
		meta:           meta,
		hasFrontmatter: hasFrontmatter,
	}
	return file, nil
}

func PatchFileContent(content string, patch PreferencePatch) (string, PreferenceFile, error) {
	file, err := ParseFileContent(content)
	if err != nil {
		return "", PreferenceFile{}, err
	}
	if file.meta == nil {
		file.meta = emptyPreferenceMetaNode()
	}
	file.hasFrontmatter = true
	if patch.Content != nil {
		file.Content = *patch.Content
	}
	if patch.AgentPersona != nil {
		file.AgentPersona = *patch.AgentPersona
		setYAMLString(file.meta, "agent_persona", *patch.AgentPersona)
	}
	if patch.PreferredName != nil {
		file.PreferredName = *patch.PreferredName
		setYAMLString(file.meta, "preferred_name", *patch.PreferredName)
	}
	if patch.ResponseStyle != nil {
		file.ResponseStyle = *patch.ResponseStyle
		setYAMLString(file.meta, "response_style", *patch.ResponseStyle)
	}
	next := serializePreferenceFile(file)
	parsed, err := ParseFileContent(next)
	if err != nil {
		return "", PreferenceFile{}, err
	}
	return next, parsed, nil
}

func splitPreferenceFrontmatter(content string) (*yaml.Node, string, bool, error) {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) == 0 || trimLineBreak(lines[0]) != "---" {
		return nil, content, false, nil
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if trimLineBreak(lines[i]) == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return nil, "", true, fmt.Errorf("invalid preference frontmatter: missing closing delimiter")
	}
	rawYAML := strings.Join(lines[1:closing], "")
	body := strings.Join(lines[closing+1:], "")
	if strings.HasPrefix(body, "\n") {
		body = strings.TrimPrefix(body, "\n")
	}
	var doc yaml.Node
	if strings.TrimSpace(rawYAML) == "" {
		return emptyPreferenceMetaNode(), body, true, nil
	}
	if err := yaml.Unmarshal([]byte(rawYAML), &doc); err != nil {
		return nil, "", true, fmt.Errorf("invalid preference frontmatter yaml: %w", err)
	}
	if len(doc.Content) == 0 {
		return emptyPreferenceMetaNode(), body, true, nil
	}
	meta := doc.Content[0]
	if meta.Kind != yaml.MappingNode {
		return nil, "", true, fmt.Errorf("invalid preference frontmatter: metadata must be a mapping")
	}
	return meta, body, true, nil
}

func serializePreferenceFile(file PreferenceFile) string {
	meta := file.meta
	if meta == nil {
		meta = emptyPreferenceMetaNode()
	}
	ensureYAMLString(meta, "agent_persona", file.AgentPersona)
	ensureYAMLString(meta, "preferred_name", file.PreferredName)
	ensureYAMLString(meta, "response_style", file.ResponseStyle)
	yamlText := marshalYAMLNode(meta)
	if yamlText != "" && !strings.HasSuffix(yamlText, "\n") {
		yamlText += "\n"
	}
	if file.Content == "" {
		return "---\n" + yamlText + "---\n"
	}
	return "---\n" + yamlText + "---\n\n" + file.Content
}

func emptyPreferenceMetaNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func yamlString(meta *yaml.Node, key string) string {
	if meta == nil || meta.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(meta.Content); i += 2 {
		if meta.Content[i].Value == key {
			return meta.Content[i+1].Value
		}
	}
	return ""
}

func ensureYAMLString(meta *yaml.Node, key, fallback string) {
	if yamlHasKey(meta, key) {
		return
	}
	setYAMLString(meta, key, fallback)
}

func setYAMLString(meta *yaml.Node, key, value string) {
	if meta.Kind == 0 {
		meta.Kind = yaml.MappingNode
		meta.Tag = "!!map"
	}
	for i := 0; i+1 < len(meta.Content); i += 2 {
		if meta.Content[i].Value == key {
			meta.Content[i+1] = yamlScalar(value)
			return
		}
	}
	meta.Content = append(meta.Content, yamlKey(key), yamlScalar(value))
}

func yamlHasKey(meta *yaml.Node, key string) bool {
	if meta == nil || meta.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(meta.Content); i += 2 {
		if meta.Content[i].Value == key {
			return true
		}
	}
	return false
}

func yamlKey(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func yamlScalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle}
}

func marshalYAMLNode(meta *yaml.Node) string {
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	_ = encoder.Encode(meta)
	_ = encoder.Close()
	out := buf.String()
	out = strings.TrimSuffix(out, "...\n")
	return out
}

func trimLineBreak(line string) string {
	return strings.TrimRight(line, "\r\n")
}
