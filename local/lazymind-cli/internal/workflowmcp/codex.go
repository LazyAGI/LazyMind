package workflowmcp

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var codexThreadPattern = regexp.MustCompile(`(?i)^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// MCP transport session IDs are not native conversation IDs. Only explicit
// thread fields are recognized. Agent-supplied fallback is a documented
// queue-integration limitation, not proof of host identity.
func codexDriver(request *mcp.CallToolRequest, fallback string) (string, error) {
	var identities []string
	if request != nil && request.Params != nil {
		meta := request.Params.Meta
		nested, _ := meta["x-codex-turn-metadata"].(map[string]any)
		if encoded, ok := meta["x-codex-turn-metadata"].(string); ok {
			if err := json.Unmarshal([]byte(encoded), &nested); err != nil {
				return "", errors.New("invalid Codex thread metadata")
			}
		}
		for _, fields := range []map[string]any{meta, nested} {
			for _, key := range []string{"threadId", "thread_id"} {
				if value, exists := fields[key]; exists {
					id, ok := value.(string)
					if !ok || !codexThreadPattern.MatchString(id) {
						return "", errors.New("invalid Codex thread UUID in host metadata")
					}
					identities = append(identities, strings.ToLower(id))
				}
			}
		}
	}
	if len(identities) > 0 {
		for _, id := range identities[1:] {
			if id != identities[0] {
				return "", errors.New("conflicting Codex thread metadata")
			}
		}
		return identities[0], nil
	}
	fallback = strings.TrimSpace(fallback)
	if !codexThreadPattern.MatchString(fallback) {
		return "", errors.New("current Codex thread UUID is unavailable; obtain CODEX_THREAD_ID from the current host shell and pass driver_session_id")
	}
	return strings.ToLower(fallback), nil
}
