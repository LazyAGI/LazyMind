package chat

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"lazymind/core/common/orm"
)

func TestChatProviderLogoFollowsOwnedAndSharedVisibility(t *testing.T) {
	db := newChatModelTestDB(t).DB
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	logo := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	own := seedAvailableChatModel(t, db, "viewer", "provider-own", "group-own", "model-own", "OpenAI", "own", "gpt-own", "llm", true, "fixture-key")
	shared := seedAvailableChatModel(t, db, "owner", "provider-shared", "group-shared", "model-shared", "OpenAI", "shared", "gpt-shared", "llm", true, "fixture-key")
	private := seedAvailableChatModel(t, db, "private-owner", "provider-private", "group-private", "model-private", "OpenAI", "private", "gpt-private", "llm", true, "fixture-key")
	for _, m := range []orm.UserModelProviderGroupModel{own, shared, private} {
		if err := db.Model(&orm.UserModelProvider{}).Where("id = ?", m.UserModelProviderID).Update("logo_data_url", logo).Error; err != nil {
			t.Fatal(err)
		}
	}
	seedSelectedChatModel(t, db, "viewer", own.ID, false)
	seedSelectedChatModel(t, db, "owner", shared.ID, true)
	response, err := buildChatModelsResponse(t.Context(), db, "viewer", &orm.Conversation{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(response)
	var payload struct {
		Providers []struct {
			LogoDataURL string           `json:"logo_data_url"`
			Models      []map[string]any `json:"models"`
		} `json:"providers"`
		Default map[string]any `json:"default_selection"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, provider := range payload.Providers {
		if provider.LogoDataURL != logo {
			t.Fatal("chat supplier lost its logo")
		}
		for _, model := range provider.Models {
			id := model["id"].(string)
			seen[id] = true
			if model["logo_data_url"] != logo {
				t.Fatalf("%s lost personal logo", id)
			}
		}
	}
	if !seen[own.ID] || !seen[shared.ID] || seen[private.ID] {
		t.Fatalf("logo metadata changed model visibility: %+v", seen)
	}
	if payload.Default["model_id"] != own.ID || payload.Default["logo_data_url"] != logo {
		t.Fatal("current/default selected model logo missing")
	}
	cfg, err := LoadDefaultChatLLMConfig(t.Context(), db, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cfg["logo_data_url"]; exists {
		t.Fatal("presentation metadata leaked into runtime configuration")
	}
}
