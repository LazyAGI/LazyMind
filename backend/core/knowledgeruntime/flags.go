package knowledgeruntime

import (
	"os"
	"strings"
)

// Enabled keeps public registration and trusted Chat/Work loading on one policy.
// An explicit false is the deployment rollback switch; invalid values fail closed.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LAZYMIND_KNOWLEDGE_MANAGEMENT_ENABLED"))) {
	case "", "true", "1":
		return true
	default:
		return false
	}
}
