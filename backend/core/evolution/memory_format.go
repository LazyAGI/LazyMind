package evolution

import (
	"errors"
	"fmt"
	"strings"

	"lazymind/core/common/orm"

	"gopkg.in/yaml.v3"
)

func FormatSystemMemoryForChat(row orm.SystemMemory) string {
	return row.Content
}

func HashSystemMemory(row orm.SystemMemory) string {
	return HashContent(row.Content)
}

func FormatSystemUserPreferenceForChat(row orm.SystemUserPreference) string {
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLFrontMatterBlock(&b, "agent_persona", row.AgentPersona)
	writeYAMLFrontMatterBlock(&b, "user_address", row.UserAddress)
	writeYAMLFrontMatterBlock(&b, "response_style", row.ResponseStyle)
	b.WriteString("---\n\n")
	b.WriteString(row.Content)
	return b.String()
}

func HashSystemUserPreference(row orm.SystemUserPreference) string {
	return HashContent(FormatSystemUserPreferenceForChat(row))
}

type userPreferenceFrontmatter struct {
	AgentPersona  string `yaml:"agent_persona"`
	UserAddress   string `yaml:"user_address"`
	ResponseStyle string `yaml:"response_style"`
}

func ParseSystemUserPreferenceContent(content string) (orm.SystemUserPreference, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return orm.SystemUserPreference{}, errors.New("user_preference content must start with YAML frontmatter")
	}
	rest := strings.TrimPrefix(content, "---\n")
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return orm.SystemUserPreference{}, errors.New("user_preference content must contain closing frontmatter separator")
	}
	yamlPart := rest[:idx]
	body := strings.TrimSpace(rest[idx+5:])
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(yamlPart), &raw); err != nil {
		return orm.SystemUserPreference{}, fmt.Errorf("invalid user_preference frontmatter: %w", err)
	}
	for _, key := range []string{"agent_persona", "user_address", "response_style"} {
		if _, ok := raw[key]; !ok {
			return orm.SystemUserPreference{}, fmt.Errorf("user_preference frontmatter %s required", key)
		}
	}
	var meta userPreferenceFrontmatter
	if err := yaml.Unmarshal([]byte(yamlPart), &meta); err != nil {
		return orm.SystemUserPreference{}, fmt.Errorf("invalid user_preference frontmatter: %w", err)
	}
	row := orm.SystemUserPreference{
		Content:       body,
		AgentPersona:  strings.TrimSpace(meta.AgentPersona),
		UserAddress:   strings.TrimSpace(meta.UserAddress),
		ResponseStyle: strings.TrimSpace(meta.ResponseStyle),
	}
	if row.Content == "" && row.AgentPersona == "" && row.UserAddress == "" && row.ResponseStyle == "" {
		return orm.SystemUserPreference{}, errors.New("user_preference content or metadata required")
	}
	return row, nil
}

func writeYAMLFrontMatterBlock(b *strings.Builder, key, value string) {
	b.WriteString(key)
	if value == "" {
		b.WriteString(": \"\"\n")
		return
	}
	b.WriteString(": |-\n")
	for _, line := range strings.Split(value, "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
}
