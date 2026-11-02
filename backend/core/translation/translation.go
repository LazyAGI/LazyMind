package translation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/modelconfig"
	"lazymind/core/modelprovider"
	"lazymind/core/store"
)

type request struct {
	Text   string `json:"text"`
	Target string `json:"target,omitempty"`
	Source string `json:"source,omitempty"`
}

type Response struct {
	TranslatedText string `json:"translated_text"`
	Source         string `json:"source"`
	Target         string `json:"target"`
}

type statusResponse struct {
	Configured        bool                     `json:"configured"`
	Provider          string                   `json:"provider,omitempty"`
	SelectedGroupID   string                   `json:"selected_group_id,omitempty"`
	DocumentSupported bool                     `json:"document_supported"`
	DocumentProviders []documentProviderOption `json:"document_providers"`
}

type documentProviderOption struct {
	GroupID      string `json:"group_id"`
	ProviderName string `json:"provider_name"`
	GroupName    string `json:"group_name"`
}

func documentProviderOptions(groups []modelconfig.TranslationProviderGroup) []documentProviderOption {
	options := make([]documentProviderOption, 0, len(groups))
	for _, group := range groups {
		spec, ok := lookupProvider(group.ProviderName)
		if !ok || !spec.SupportsDocuments {
			continue
		}
		options = append(options, documentProviderOption{
			GroupID: group.GroupID, ProviderName: spec.Name, GroupName: group.GroupName,
		})
	}
	return options
}

// DocumentProviderSnapshot resolves one user's verified connection for a
// document job without changing their default translation source.
func DocumentProviderSnapshot(ctx context.Context, db *gorm.DB, userID, groupID string) (*modelconfig.TranslationConfigSnapshot, bool, error) {
	groups, err := modelconfig.ListTranslationProviderGroups(ctx, db, userID)
	if err != nil {
		return nil, false, err
	}
	for _, group := range groups {
		if group.GroupID != strings.TrimSpace(groupID) {
			continue
		}
		spec, ok := lookupProvider(group.ProviderName)
		if !ok || !spec.SupportsDocuments {
			return nil, false, nil
		}
		snapshot := group.Snapshot
		return &snapshot, true, nil
	}
	return nil, false, nil
}

// Initialize provisions free connections explicitly, outside read-only status requests.
func Initialize(w http.ResponseWriter, r *http.Request) {
	if err := modelprovider.EnsureDefaultTranslationServices(r.Context(), store.DB(), store.UserID(r)); err != nil {
		common.ReplyErr(w, "initialize free translation services failed", http.StatusInternalServerError)
		return
	}
	Status(w, r)
}

func Status(w http.ResponseWriter, r *http.Request) {
	groups, err := modelconfig.ListTranslationProviderGroups(r.Context(), store.DB(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, "load translation providers failed", http.StatusInternalServerError)
		return
	}
	response := statusResponse{DocumentProviders: documentProviderOptions(groups)}
	config, err := modelconfig.LoadTranslationConfig(r.Context(), store.DB(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, "load translation config failed", http.StatusInternalServerError)
		return
	}
	if config == nil {
		common.ReplyOK(w, response)
		return
	}
	spec, ok := lookupProvider(config.ProviderName)
	response.Configured, response.Provider, response.DocumentSupported = ok, spec.Name, ok && spec.SupportsDocuments
	selected, err := modelconfig.LoadTranslationConfigSnapshot(r.Context(), store.DB(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, "load translation selection failed", http.StatusInternalServerError)
		return
	}
	if selected != nil {
		response.SelectedGroupID = selected.GroupID
	}
	common.ReplyOK(w, response)
}

func Translate(w http.ResponseWriter, r *http.Request) {
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		common.ReplyErr(w, "text is required", http.StatusBadRequest)
		return
	}
	if len([]rune(req.Text)) > 5000 {
		common.ReplyErr(w, "text exceeds 5000 characters", http.StatusBadRequest)
		return
	}

	config, err := modelconfig.LoadTranslationConfig(r.Context(), store.DB(), store.UserID(r))
	if err == nil && config == nil {
		err = modelprovider.EnsureDefaultTranslationServices(r.Context(), store.DB(), store.UserID(r))
		if err == nil {
			config, err = modelconfig.LoadTranslationConfig(r.Context(), store.DB(), store.UserID(r))
		}
	}

	if err != nil {
		common.ReplyErr(w, "load translation config failed", http.StatusInternalServerError)
		return
	}
	if config == nil {
		common.ReplyErr(w, "translation service is not configured", http.StatusPreconditionFailed)
		return
	}
	spec, ok := lookupProvider(config.ProviderName)
	if !ok {
		common.ReplyErr(w, "unsupported translation provider", http.StatusBadRequest)
		return
	}
	if !spec.Keyless && strings.TrimSpace(config.APIKey) == "" {
		common.ReplyErr(w, "invalid translation credentials", http.StatusPreconditionFailed)
		return
	}
	result, err := translateWithConfigSource(r.Context(), nil, store.UserID(r), *config, req.Text, req.Source, req.Target)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadGateway)
		return
	}
	common.ReplyOK(w, result)
}

// TranslateText exposes the configured translation provider to other Core
// domains without routing an internal request through HTTP.
func TranslateText(ctx context.Context, userID, text, target string) (Response, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Response{}, errors.New("text is required")
	}
	config, err := modelconfig.LoadTranslationConfig(ctx, store.DB(), userID)
	if err != nil {
		return Response{}, err
	}
	if config == nil {
		return Response{}, errors.New("translation service is not configured")
	}
	return translateWithConfig(ctx, nil, userID, *config, text, target)
}

// SelectedProvider reports the chosen API source without exposing credentials.
func SelectedProvider(ctx context.Context, userID string) (string, bool, error) {
	config, err := modelconfig.LoadTranslationConfig(ctx, store.DB(), userID)
	if err != nil || config == nil {
		return "", false, err
	}
	spec, ok := lookupProvider(config.ProviderName)
	if !ok {
		return "", false, errors.New("unsupported translation provider")
	}
	return spec.Name, spec.SupportsDocuments, nil
}

// SelectedProviderSnapshot captures the selected provider and encrypted
// credentials in one database read for a long-running document job.
func SelectedProviderSnapshot(ctx context.Context, userID string) (*modelconfig.TranslationConfigSnapshot, bool, error) {
	snapshot, err := modelconfig.LoadTranslationConfigSnapshot(ctx, store.DB(), userID)
	if err != nil || snapshot == nil {
		return nil, false, err
	}
	spec, ok := lookupProvider(snapshot.ProviderName)
	if !ok {
		return nil, false, errors.New("unsupported translation provider")
	}
	return snapshot, spec.SupportsDocuments, nil
}

// TranslateTextWithSnapshot checks a pinned provider group on every document
// chunk, so switching selections is harmless and deleting credentials revokes
// remaining work.
func TranslateTextWithSnapshot(ctx context.Context, userID string, snapshot modelconfig.TranslationConfigSnapshot, text, target string) (Response, error) {
	config, err := modelconfig.ResolveTranslationConfigSnapshot(ctx, store.DB(), userID, snapshot)
	if err != nil {
		return Response{}, err
	}
	return translateWithConfig(ctx, nil, userID, config, text, target, func() error {
		_, err := modelconfig.ResolveTranslationConfigSnapshot(ctx, store.DB(), userID, snapshot)
		return err
	})
}

func normalizeTarget(target, text string) string {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "zh" || target == "en" {
		return target
	}
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return "en"
		}
	}
	return "zh"
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
