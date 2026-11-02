package chat

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

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

func chatProviderLogoFixture(t *testing.T, pixel color.NRGBA) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, pixel)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
}

func TestAutoChatProviderLogoFollowsCurrentSupplier(t *testing.T) {
	db := newChatModelTestDB(t).DB
	// Another usable model sorts first, while the snapshot identifies the second.
	seedAvailableChatModel(t, db, "viewer", "provider-first", "group-first", "model-first", "DeepSeek", "first", "first", "llm", true, "fixture-key")
	seedAvailableChatModel(t, db, "viewer", "provider-latest", "group-latest", "model-latest", "OpenAI", "latest", "latest", "llm", true, "fixture-key")
	mode := chatModelModeAuto
	conversation := orm.Conversation{
		ChatModelMode: &mode, ChatModelVersion: 3,
		ChatModelSnapshot: json.RawMessage(`{"model_id":"model-latest","provider_id":"provider-latest","provider_name":"OpenAI","provider_group_id":"group-latest","group_name":"latest","model_name":"latest","source":"own","successful_run_id":"completed-run"}`),
	}
	originalSnapshot := append([]byte(nil), conversation.ChatModelSnapshot...)
	for _, tc := range []struct {
		name string
		logo string
	}{
		{"uploaded", chatProviderLogoFixture(t, color.NRGBA{R: 255, A: 255})},
		{"changed", chatProviderLogoFixture(t, color.NRGBA{B: 255, A: 255})},
		{"restored default", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := db.Model(&orm.UserModelProvider{}).Where("id = ?", "provider-latest").Updates(map[string]any{
				"logo_data_url": tc.logo, "name": "My gateway", "is_custom": true, "protocol_name": "OpenAI",
			}).Error; err != nil {
				t.Fatal(err)
			}
			response, err := buildChatModelsResponse(t.Context(), db, "viewer", &conversation)
			if err != nil {
				t.Fatal(err)
			}
			if response.Selection.LogoDataURL != tc.logo {
				t.Fatalf("Auto latest supplier logo=%q, want current logo=%q", response.Selection.LogoDataURL, tc.logo)
			}
			if response.Selection.Mode != "auto" || response.Selection.ModelID != "" || response.Selection.Version != 3 || response.Selection.Availability != "available" {
				t.Fatalf("logo changed Auto selection semantics: %+v", response.Selection)
			}
			if !bytes.Equal(conversation.ChatModelSnapshot, originalSnapshot) {
				t.Fatal("display logo was written into the persisted model snapshot")
			}
		})
	}
}

func TestAutoChatProviderLogoRequiresExactVisibleSnapshotIdentity(t *testing.T) {
	mode := chatModelModeAuto
	logo := chatProviderLogoFixture(t, color.NRGBA{G: 255, A: 255})
	visible := availableChatModel{
		ID: "model-visible", ProviderID: "provider-visible", ProviderGroupID: "group-visible", Source: "shared",
		ProviderName: "OpenAI", ModelName: "same-name", LogoDataURL: logo,
	}
	for _, tc := range []struct {
		name     string
		snapshot string
		want     string
	}{
		{"exact IDs with renamed display", `{"model_id":"model-visible","provider_id":"provider-visible","provider_group_id":"group-visible","source":"shared","model_name":"old-name"}`, logo},
		{"same name different model", `{"model_id":"model-other","provider_id":"provider-visible","provider_group_id":"group-visible","source":"shared","model_name":"same-name"}`, ""},
		{"different provider", `{"model_id":"model-visible","provider_id":"provider-other","provider_group_id":"group-visible","source":"shared"}`, ""},
		{"different group", `{"model_id":"model-visible","provider_id":"provider-visible","provider_group_id":"group-other","source":"shared"}`, ""},
		{"different source", `{"model_id":"model-visible","provider_id":"provider-visible","provider_group_id":"group-visible","source":"own"}`, ""},
		{"missing model", `{"provider_id":"provider-visible","provider_group_id":"group-visible","source":"shared"}`, ""},
		{"missing provider", `{"model_id":"model-visible","provider_group_id":"group-visible","source":"shared"}`, ""},
		{"missing group", `{"model_id":"model-visible","provider_id":"provider-visible","source":"shared"}`, ""},
		{"missing source", `{"model_id":"model-visible","provider_id":"provider-visible","provider_group_id":"group-visible"}`, ""},
		{"invalid snapshot", `{"model_id":`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selection := resolvedSelectionForConversation(&orm.Conversation{
				ChatModelMode: &mode, ChatModelSnapshot: json.RawMessage(tc.snapshot),
			}, []availableChatModel{visible}, nil)
			if selection.LogoDataURL != tc.want {
				t.Fatalf("supplier logo=%q, want %q", selection.LogoDataURL, tc.want)
			}
		})
	}
}

func TestAutoChatProviderLogoDisappearsWhenSharedModelIsNoLongerVisible(t *testing.T) {
	for _, removed := range []string{"share revoked", "supplier removed"} {
		t.Run(removed, func(t *testing.T) {
			db := newChatModelTestDB(t).DB
			seedAvailableChatModel(t, db, "owner", "provider-shared", "group-shared", "model-shared", "OpenAI", "shared", "same-name", "llm", true, "fixture-key")
			seedSelectedChatModel(t, db, "owner", "model-shared", true)
			logo := chatProviderLogoFixture(t, color.NRGBA{R: 255, G: 255, A: 255})
			if err := db.Model(&orm.UserModelProvider{}).Where("id = ?", "provider-shared").Update("logo_data_url", logo).Error; err != nil {
				t.Fatal(err)
			}
			mode := chatModelModeAuto
			conversation := orm.Conversation{
				ChatModelMode:     &mode,
				ChatModelSnapshot: json.RawMessage(`{"model_id":"model-shared","provider_id":"provider-shared","provider_group_id":"group-shared","model_name":"same-name","source":"shared"}`),
			}
			before, err := buildChatModelsResponse(t.Context(), db, "viewer", &conversation)
			if err != nil || before.Selection.LogoDataURL != logo {
				t.Fatalf("visible shared logo missing: %+v, %v", before.Selection, err)
			}
			if removed == "share revoked" {
				err = db.Model(&orm.UserSelectedModel{}).Where("user_id = ?", "owner").Update("share", false).Error
			} else {
				err = db.Model(&orm.UserModelProvider{}).Where("id = ?", "provider-shared").Update("deleted_at", time.Now()).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			after, err := buildChatModelsResponse(t.Context(), db, "viewer", &conversation)
			if err != nil {
				t.Fatal(err)
			}
			if after.Selection.LogoDataURL != "" || len(after.Providers) != 0 {
				t.Fatalf("hidden shared supplier logo or models leaked: %+v", after)
			}
		})
	}
}
