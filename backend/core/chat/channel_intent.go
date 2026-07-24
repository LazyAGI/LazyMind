package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

const (
	maxChannelIntentBodyBytes     = 64 << 10
	maxChannelIntentStateBytes    = 16 << 10
	maxChannelCommandRegistrySize = 48 << 10
	maxChannelOutputSchemaBytes   = 40 << 10
	maxChannelParametersBytes     = 48 << 10
	maxChannelCommands            = 32
)

type channelIntentInput struct {
	Provider        string                       `json:"provider"`
	Message         string                       `json:"message"`
	State           json.RawMessage              `json:"state"`
	CommandRegistry *algo.ChannelCommandRegistry `json:"command_registry"`
}

// ClassifyChannelIntent authenticates the caller, injects its model selection,
// and proxies an opaque, caller-defined command registry to the algorithm service.
func ClassifyChannelIntent(w http.ResponseWriter, r *http.Request) {
	ownerUserID := strings.TrimSpace(store.UserID(r))
	if ownerUserID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var input channelIntentInput
	reader := http.MaxBytesReader(w, r.Body, maxChannelIntentBodyBytes)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		status := http.StatusBadRequest
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			status = http.StatusRequestEntityTooLarge
		}
		common.ReplyErr(w, "invalid channel intent request", status)
		return
	}
	if err := ensureChannelIntentEOF(decoder); err != nil {
		common.ReplyErr(w, "invalid channel intent request", http.StatusBadRequest)
		return
	}
	if !validChannelIntentText(input.Provider, input.Message) {
		common.ReplyErr(w, "invalid channel intent request", http.StatusBadRequest)
		return
	}
	if len(input.State) == 0 {
		input.State = json.RawMessage(`{}`)
	}
	if !validJSONObject(input.State, maxChannelIntentStateBytes) {
		common.ReplyErr(w, "invalid channel intent state", http.StatusBadRequest)
		return
	}
	if input.CommandRegistry == nil || !validChannelCommandRegistry(*input.CommandRegistry) {
		common.ReplyErr(w, "invalid channel command registry", http.StatusBadRequest)
		return
	}

	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	llmConfig, err := modelconfig.LoadLLMConfig(r.Context(), db, ownerUserID)
	if err != nil {
		common.ReplyErr(w, "load model config failed", http.StatusInternalServerError)
		return
	}
	llmRole, ok := llmConfig["llm"].(map[string]any)
	if !ok || len(llmRole) == 0 {
		common.ReplyErr(w, "no chat model configured", http.StatusConflict)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()
	envelope, err := algo.ClassifyChannelIntent(ctx, algo.ChannelIntentRequest{
		Provider:        input.Provider,
		Message:         input.Message,
		State:           input.State,
		CommandRegistry: *input.CommandRegistry,
		LLMConfig:       map[string]any{"llm": llmRole},
	})
	if err != nil {
		common.ReplyErr(w, "channel intent classification failed", http.StatusBadGateway)
		return
	}
	if !validChannelCommandEnvelope(envelope, *input.CommandRegistry) {
		common.ReplyErr(w, "invalid channel intent response", http.StatusBadGateway)
		return
	}
	common.ReplyOK(w, envelope)
}

func validChannelIntentText(provider, message string) bool {
	return strings.TrimSpace(provider) != "" &&
		utf8.RuneCountInString(provider) <= 32 &&
		strings.TrimSpace(message) != "" &&
		utf8.RuneCountInString(message) <= 4000
}

func validChannelCommandRegistry(registry algo.ChannelCommandRegistry) bool {
	if strings.TrimSpace(registry.SchemaVersion) == "" ||
		utf8.RuneCountInString(registry.SchemaVersion) > 32 ||
		len(registry.Commands) == 0 ||
		len(registry.Commands) > maxChannelCommands ||
		len(registry.SelectionRules) == 0 ||
		len(registry.SelectionRules) > 16 ||
		!validJSONObject(registry.OutputSchema, maxChannelOutputSchemaBytes) {
		return false
	}
	encoded, err := json.Marshal(registry)
	if err != nil || len(encoded) > maxChannelCommandRegistrySize {
		return false
	}
	seen := make(map[string]struct{}, len(registry.Commands))
	for _, command := range registry.Commands {
		if !validChannelCommandName(command.Name) ||
			strings.TrimSpace(command.Description) == "" ||
			utf8.RuneCountInString(command.Description) > 2000 {
			return false
		}
		if _, exists := seen[command.Name]; exists {
			return false
		}
		seen[command.Name] = struct{}{}
	}
	for _, rule := range registry.SelectionRules {
		if strings.TrimSpace(rule) == "" || utf8.RuneCountInString(rule) > 1000 {
			return false
		}
	}
	return true
}

func validChannelCommandEnvelope(
	envelope algo.ChannelCommandEnvelope,
	registry algo.ChannelCommandRegistry,
) bool {
	if envelope.SchemaVersion != registry.SchemaVersion ||
		!validJSONObject(envelope.Parameters, maxChannelParametersBytes) {
		return false
	}
	for _, command := range registry.Commands {
		if envelope.Command == command.Name {
			return true
		}
	}
	return false
}

func validChannelCommandName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func validJSONObject(raw json.RawMessage, maxBytes int) bool {
	if len(raw) == 0 || len(raw) > maxBytes {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return false
	}
	return ensureChannelIntentEOF(decoder) == nil
}

func ensureChannelIntentEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}
