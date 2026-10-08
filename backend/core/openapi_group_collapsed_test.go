package main

import "testing"

func TestConversationGroupCollapsedContract(t *testing.T) {
	schemas := conversationGroupSchemas()
	group := schemas["ConversationGroup"].(map[string]any)
	collapsed := group["properties"].(map[string]any)["collapsed"].(map[string]any)
	if collapsed["type"] != "boolean" || collapsed["default"] != false {
		t.Fatalf("invalid collapse schema: %v", collapsed)
	}
	placement := schemas["ConversationGroupPlacementRequest"].(map[string]any)["properties"].(map[string]any)
	if placement["collapsed"].(map[string]any)["type"] != "boolean" {
		t.Fatal("placement must accept a boolean collapse state")
	}
}
