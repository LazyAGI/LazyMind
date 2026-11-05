package artifact

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConversationAgentWorkspaceRoots mirrors the main-Agent file resource scope.
// The caller must first authorize the conversation for the owner.
func ConversationAgentWorkspaceRoots(owner, conversation string) []string {
	root := strings.TrimSpace(os.Getenv("LAZYMIND_AGENTIC_WORKSPACE"))
	if root == "" || owner == "" || conversation == "" {
		return nil
	}
	u, c := sha256.Sum256([]byte(owner)), sha256.Sum256([]byte(conversation))
	return []string{
		filepath.Join(root, "chat-artifacts", fmt.Sprintf("%x", u[:16]), fmt.Sprintf("%x", c[:16])),
		filepath.Join(root, "chat-artifacts", fmt.Sprintf("%x", u[:]), fmt.Sprintf("%x", c[:])),
	}
}
