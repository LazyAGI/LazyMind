package modelprovider

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
	"lazymind/core/store"
)

type savedProviderRequestConfig struct {
	DefaultsVersion    int                                      `json:"defaults_version,omitempty"`
	DefaultProtocol    string                                   `json:"default_protocol"`
	Endpoints          map[string]string                        `json:"endpoints"`
	Defaults           map[string]string                        `json:"defaults"`
	Advanced           *credentialvault.ProviderRequestAdvanced `json:"advanced,omitempty"`
	AdvancedCiphertext string                                   `json:"advanced_ciphertext,omitempty"`
}

type providerRequestProtocol struct {
	Protocol       string `json:"protocol"`
	Name           string `json:"name"`
	BaseURL        string `json:"base_url"`
	DefaultBaseURL string `json:"default_base_url"`
	Supported      bool   `json:"supported"`
}

type providerRequestConfigResponse struct {
	DefaultProtocol string                                  `json:"default_protocol"`
	Protocols       []providerRequestProtocol               `json:"protocols"`
	Advanced        credentialvault.ProviderRequestAdvanced `json:"advanced"`
}

type updateProviderRequestConfigRequest struct {
	DefaultProtocol string                                   `json:"default_protocol"`
	Endpoints       map[string]string                        `json:"endpoints"`
	Advanced        *credentialvault.ProviderRequestAdvanced `json:"advanced,omitempty"`
}

func defaultRequestProtocol(provider orm.UserModelProvider) string {
	if LazyLLMSource(RuntimeProviderName(provider)) == "claude" {
		return "anthropic"
	}
	return "openai"
}

func providerRequestDefaults(db *gorm.DB, parent orm.UserModelProvider) map[string]string {
	base := strings.TrimSpace(parent.BaseURL)
	// A catalog entry is a fallback for an unconfigured supplier, never a reason
	// to send a proxy supplier's credentials to a different host.
	if base == "" && db != nil && db.Migrator().HasTable(&orm.DefaultModelProvider{}) {
		var catalog orm.DefaultModelProvider
		if db.Where("id = ? AND deleted_at IS NULL", parent.DefaultModelProviderID).Take(&catalog).Error == nil && strings.TrimSpace(catalog.BaseURL) != "" {
			base = catalog.BaseURL
		}
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		return map[string]string{"openai": base, "anthropic": base}
	}
	root := *parsed
	rawPath := strings.TrimRight(root.EscapedPath(), "/")
	lastSlash := strings.LastIndex(rawPath, "/")
	lastSegment, _ := url.PathUnescape(rawPath[lastSlash+1:])
	if strings.EqualFold(lastSegment, "v1") {
		rawPath = rawPath[:lastSlash]
	}
	root.Path, _ = url.PathUnescape(rawPath)
	root.RawPath = rawPath
	// Both adapters append request paths. Only the version suffix belongs to
	// the protocol; tenant/reverse-proxy prefixes still belong to the supplier.
	rootURL := root.String()
	openai := base
	if defaultRequestProtocol(parent) == "anthropic" {
		openai = rootURL + "/v1/"
	}
	defaults := map[string]string{"openai": openai, "anthropic": rootURL, "gemini": rootURL + "/gemini/v1beta", "openai-responses": openai}
	if known, ok := supplierProtocolDefaults(parent, base); ok {
		defaults["openai"], defaults["anthropic"] = known["openai"], known["anthropic"]
		defaults["openai-responses"] = known["openai"]
	}
	return defaults
}

func savedRequestConfig(db *gorm.DB, parent orm.UserModelProvider) savedProviderRequestConfig {
	config := savedProviderRequestConfig{DefaultsVersion: 1, DefaultProtocol: defaultRequestProtocol(parent), Endpoints: map[string]string{}, Defaults: providerRequestDefaults(db, parent)}
	var stored savedProviderRequestConfig
	if parent.RequestConfig != "" && json.Unmarshal([]byte(parent.RequestConfig), &stored) == nil {
		config.Advanced, config.AdvancedCiphertext = stored.Advanced, stored.AdvancedCiphertext
		if stored.DefaultProtocol == "openai" || stored.DefaultProtocol == "anthropic" {
			config.DefaultProtocol = stored.DefaultProtocol
		}
		for key, value := range stored.Endpoints {
			config.Endpoints[key] = value
		}
		if stored.DefaultsVersion == 1 {
			for key, value := range stored.Defaults {
				if value != "" {
					config.Defaults[key] = value
				}
			}
			repairInheritedProtocolDefaults(&config, stored, parent)
		} else {
			// Old saves derived defaults from the catalog/origin and copied those
			// defaults into endpoints. The saved OpenAI address retains the original
			// supplier when an explicit Anthropic override uses a different path/host.
			anchor := stored.Endpoints["openai"]
			if anchor == "" {
				anchor = stored.Defaults["openai"]
			}
			anchorProvider := parent
			if anchor != "" {
				anchorProvider.BaseURL, anchorProvider.ProtocolName = anchor, "OpenAI"
			}
			if config.DefaultProtocol == "openai" || originalRequestProtocolIsAnthropic(db, parent) && sameRequestOrigin(anchor, parent.BaseURL) {
				anchorProvider = parent
			}
			config.Defaults = providerRequestDefaults(db, anchorProvider)
			for protocol, endpoint := range config.Endpoints {
				if endpoint != "" && normalizeBaseURLForCompare(endpoint) == normalizeBaseURLForCompare(stored.Defaults[protocol]) {
					config.Endpoints[protocol] = ""
				}
			}
		}
	}
	for protocol, endpoint := range config.Endpoints {
		if endpoint != "" && normalizeBaseURLForCompare(endpoint) == normalizeBaseURLForCompare(config.Defaults[protocol]) {
			config.Endpoints[protocol] = ""
		}
	}
	return config
}

func originalRequestProtocolIsAnthropic(db *gorm.DB, parent orm.UserModelProvider) bool {
	// ProtocolName changes when users select a default. The immutable template
	// identifies a genuinely native Anthropic root without mistaking an explicit
	// /anthropic proxy override for the original supplier's API prefix.
	if db != nil && parent.DefaultModelProviderID != "" && db.Migrator().HasTable(&orm.DefaultModelProvider{}) {
		var template orm.DefaultModelProvider
		if db.Where("id = ?", parent.DefaultModelProviderID).Take(&template).Error == nil {
			return LazyLLMSource(RuntimeProviderName(orm.UserModelProvider{Name: template.Name})) == "claude"
		}
	}
	return parent.ProtocolName == "" && LazyLLMSource(RuntimeProviderName(parent)) == "claude"
}

func sameRequestOrigin(left, right string) bool {
	a, errA := url.Parse(left)
	b, errB := url.Parse(right)
	return errA == nil && errB == nil && a.Host != "" && b.Host != "" && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func requestConfigResponse(db *gorm.DB, parent orm.UserModelProvider) providerRequestConfigResponse {
	config := savedRequestConfig(db, parent)
	response := providerRequestConfigResponse{DefaultProtocol: config.DefaultProtocol, Protocols: []providerRequestProtocol{}}
	for _, item := range []struct {
		key, name string
		supported bool
	}{{"openai", "OpenAI", true}, {"anthropic", "Anthropic", true}, {"gemini", "Gemini", false}, {"openai-responses", "OpenAI Responses", false}} {
		base := config.Endpoints[item.key]
		if base == "" {
			base = config.Defaults[item.key]
		}
		if item.key == config.DefaultProtocol {
			base = parent.BaseURL
		}
		response.Protocols = append(response.Protocols, providerRequestProtocol{Protocol: item.key, Name: item.name, BaseURL: base, DefaultBaseURL: config.Defaults[item.key], Supported: item.supported && serviceSupportsRequestProtocol(parent, item.key)})
	}
	return response
}

func requestConfigSupplier(w http.ResponseWriter, r *http.Request) (*gorm.DB, orm.UserModelProvider, bool) {
	db, user := store.DB(), strings.TrimSpace(store.UserID(r))
	var parent orm.UserModelProvider
	if db == nil {
		common.ReplyErr(w, "store not initialized", 500)
		return nil, parent, false
	}
	if user == "" {
		common.ReplyErr(w, "missing X-User-Id", 400)
		return nil, parent, false
	}
	if err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", strings.TrimSpace(mux.Vars(r)["model_provider_id"]), user).Take(&parent).Error; err != nil {
		common.ReplyErr(w, "model provider not found", 404)
		return nil, parent, false
	}
	if !hasSharedProviderEndpoint(parent) {
		common.ReplyErr(w, "only model suppliers have request configuration", 400)
		return nil, parent, false
	}
	return db, parent, true
}

func GetProviderRequestConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	db, parent, ok := requestConfigSupplier(w, r)
	if !ok {
		return
	}
	advanced, err := ResolveProviderRequestAdvanced(parent)
	if err != nil {
		common.ReplyErr(w, "read advanced request configuration failed", 500)
		return
	}
	response := requestConfigResponse(db.WithContext(r.Context()), parent)
	response.Advanced = advanced
	common.ReplyOK(w, response)
}

var errUnsupportedRequestModels = errors.New("Anthropic currently supports chat and vision models only; remove incompatible models before changing the default protocol")

func SetProviderRequestConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	db, parent, ok := requestConfigSupplier(w, r)
	if !ok {
		return
	}
	var req updateProviderRequestConfigRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24*1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil {
		common.ReplyErr(w, "invalid request configuration", 400)
		return
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		common.ReplyErr(w, "invalid request configuration", 400)
		return
	}
	if req.Advanced != nil {
		if err := credentialvault.ValidateProviderRequestAdvanced(*req.Advanced); err != nil {
			common.ReplyErr(w, err.Error(), 400)
			return
		}
	}
	if !serviceSupportsRequestProtocol(parent, req.DefaultProtocol) {
		common.ReplyErr(w, "request protocol is not supported by the current runtime", 400)
		return
	}
	for key, value := range req.Endpoints {
		if key != "openai" && key != "anthropic" {
			common.ReplyErr(w, "request protocol is not supported by the current runtime", 400)
			return
		}
		value = strings.TrimSpace(value)
		req.Endpoints[key] = value
		protocol := "OpenAI"
		if key == "anthropic" {
			protocol = "Claude"
		}
		if value != "" && !validCustomProviderURL(protocol, value) {
			common.ReplyErr(w, "request endpoint must be an HTTP(S) API root without credentials, query or fragment", 400)
			return
		}
	}
	now := time.Now()
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockProvider(tx, &parent); err != nil {
			return err
		}
		current := savedRequestConfig(tx, parent)
		previousAdvanced, err := ResolveProviderRequestAdvanced(parent)
		if err != nil {
			return err
		}
		advancedChanged := false
		if req.Advanced != nil {
			previousJSON, _ := json.Marshal(previousAdvanced)
			nextJSON, _ := json.Marshal(*req.Advanced)
			advancedChanged = string(previousJSON) != string(nextJSON)
			if advancedChanged {
				current.AdvancedCiphertext, err = encryptRequestAdvanced(parent, *req.Advanced)
				if err != nil {
					return err
				}
				current.Advanced = nil
			}
		}
		before := requestConfigResponse(tx, parent)
		for _, entry := range before.Protocols {
			// Preserve the currently active address while switching protocols, but
			// do not freeze an inactive protocol's derived default into an override.
			if entry.Protocol == current.DefaultProtocol && current.Endpoints[entry.Protocol] == "" {
				current.Endpoints[entry.Protocol] = entry.BaseURL
			}
		}
		for key, value := range req.Endpoints {
			if normalizeBaseURLForCompare(value) == normalizeBaseURLForCompare(current.Defaults[key]) {
				value = ""
			}
			current.Endpoints[key] = value
		}
		protocolChanged := before.DefaultProtocol != req.DefaultProtocol
		current.DefaultProtocol = req.DefaultProtocol
		effective := current.Endpoints[current.DefaultProtocol]
		if effective == "" {
			effective = current.Defaults[current.DefaultProtocol]
		}
		if strings.TrimSpace(effective) == "" {
			return errors.New("default request endpoint is empty")
		}
		protocol := RuntimeProviderName(parent)
		if protocolChanged {
			protocol = "OpenAI"
			if current.DefaultProtocol == "anthropic" {
				protocol = "Claude"
			}
		}
		if current.DefaultProtocol == "anthropic" && protocolChanged && tx.Migrator().HasTable(&orm.UserModelProviderGroupModel{}) {
			var n int64
			if err := tx.Model(&orm.UserModelProviderGroupModel{}).Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL AND model_type NOT IN ?", parent.ID, parent.CreateUserID, []string{"llm", "vlm"}).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return errUnsupportedRequestModels
			}
		}
		effective = LazyLLMBaseURL(protocol, effective)
		if !protocolChanged && normalizeBaseURLForCompare(effective) == normalizeBaseURLForCompare(parent.BaseURL) {
			effective = parent.BaseURL
		}
		current.Endpoints[current.DefaultProtocol] = effective
		if normalizeBaseURLForCompare(effective) == normalizeBaseURLForCompare(current.Defaults[current.DefaultProtocol]) {
			current.Endpoints[current.DefaultProtocol] = ""
		}
		encoded, err := json.Marshal(current)
		if err != nil {
			return err
		}
		// Saving an unchanged drawer must not change native provider adapters or
		// invalidate credentials. Persist metadata only when an endpoint changed.
		unchanged := !protocolChanged && !advancedChanged
		for _, entry := range before.Protocols {
			if entry.Supported {
				next := current.Endpoints[entry.Protocol]
				if next == "" {
					next = current.Defaults[entry.Protocol]
				}
				if normalizeBaseURLForCompare(next) != normalizeBaseURLForCompare(entry.BaseURL) {
					unchanged = false
				}
			}
		}
		if unchanged {
			return nil
		}
		if err := initializeProviderActiveKey(tx, &parent); err != nil {
			return err
		}
		parent.RequestConfig = string(encoded)
		if _, err := portableProviderRequestConfig(parent); err != nil {
			return err
		}
		if protocolChanged || advancedChanged {
			parent.ActiveKeyRevision++
		}
		if protocolChanged {
			parent.ProtocolName = protocol
		}
		if err := tx.Model(&parent).Updates(map[string]any{"request_config": parent.RequestConfig, "protocol_name": parent.ProtocolName, "active_key_revision": parent.ActiveKeyRevision, "updated_at": now}).Error; err != nil {
			return err
		}
		if protocolChanged && tx.Migrator().HasTable(&orm.UserModelProviderGroupModel{}) {
			if err := tx.Model(&orm.UserModelProviderGroupModel{}).Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", parent.ID, parent.CreateUserID).Updates(map[string]any{"provider_name": protocol, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return updateProviderEndpointLocked(tx, &parent, effective, now, true, protocolChanged || advancedChanged)
	})
	if err != nil {
		if errors.Is(err, credentialvault.ErrInvalidContract) {
			common.ReplyErr(w, "request configuration exceeds credential limits", 400)
		} else if errors.Is(err, errUnsupportedRequestModels) {
			common.ReplyErr(w, err.Error(), 400)
		} else {
			common.ReplyErr(w, "save request configuration failed", 500)
		}
		return
	}
	response := requestConfigResponse(db.WithContext(r.Context()), parent)
	response.Advanced, err = ResolveProviderRequestAdvanced(parent)
	if err != nil {
		common.ReplyErr(w, "read advanced request configuration failed", 500)
		return
	}
	common.ReplyOK(w, response)
}
