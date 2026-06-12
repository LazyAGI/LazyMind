package evolution

import (
	"strings"

	"lazymind/core/common/orm"
)

func FormatSystemMemoryForChat(row orm.SystemMemory) string {
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLFrontMatterBlock(&b, "agent_persona", row.AgentPersona)
	writeYAMLFrontMatterBlock(&b, "user_address", row.UserAddress)
	writeYAMLFrontMatterBlock(&b, "response_style", row.ResponseStyle)
	b.WriteString("---\n\n")
	b.WriteString(row.Content)
	return b.String()
}

func HashSystemMemory(row orm.SystemMemory) string {
	return HashContent(FormatSystemMemoryForChat(row))
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
