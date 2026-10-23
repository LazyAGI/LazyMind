package modelprovider

import (
	"encoding/json"
	"errors"
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
	DefaultProtocol string            `json:"default_protocol"`
	Endpoints       map[string]string `json:"endpoints"`
	Defaults        map[string]string `json:"defaults"`
}

type providerRequestProtocol struct {
	Protocol       string `json:"protocol"`
	Name           string `json:"name"`
	BaseURL        string `json:"base_url"`
	DefaultBaseURL string `json:"default_base_url"`
	Supported      bool   `json:"supported"`
}

type providerRequestConfigResponse struct {
	DefaultProtocol string                    `json:"default_protocol"`
	Protocols       []providerRequestProtocol `json:"protocols"`
}

type updateProviderRequestConfigRequest struct {
	DefaultProtocol string            `json:"default_protocol"`
	Endpoints       map[string]string `json:"endpoints"`
}

func defaultRequestProtocol(provider orm.UserModelProvider) string {
	if LazyLLMSource(RuntimeProviderName(provider)) == "claude" {
		return "anthropic"
	}
	return "openai"
}

func providerRequestDefaults(db *gorm.DB, parent orm.UserModelProvider) map[string]string {
	base := parent.BaseURL
	if !parent.IsCustom && db.Migrator().HasTable(&orm.DefaultModelProvider{}) {
		var catalog orm.DefaultModelProvider
		if db.Where("id = ? AND deleted_at IS NULL", parent.DefaultModelProviderID).Take(&catalog).Error == nil && strings.TrimSpace(catalog.BaseURL) != "" {
			base = catalog.BaseURL
		}
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		return map[string]string{"openai": base, "anthropic": base}
	}
	origin := parsed.Scheme + "://" + parsed.Host
	openai := base
	if defaultRequestProtocol(parent) == "anthropic" {
		openai = origin + "/v1/"
	}
	return map[string]string{"openai": openai, "anthropic": origin, "gemini": origin + "/gemini/v1beta", "openai-responses": openai}
}

func savedRequestConfig(db *gorm.DB, parent orm.UserModelProvider) savedProviderRequestConfig {
	config := savedProviderRequestConfig{DefaultProtocol: defaultRequestProtocol(parent), Endpoints: map[string]string{}, Defaults: providerRequestDefaults(db, parent)}
	var stored savedProviderRequestConfig
	if parent.RequestConfig != "" && json.Unmarshal([]byte(parent.RequestConfig), &stored) == nil {
		if stored.DefaultProtocol == "openai" || stored.DefaultProtocol == "anthropic" {
			config.DefaultProtocol = stored.DefaultProtocol
		}
		for key, value := range stored.Endpoints {
			config.Endpoints[key] = value
		}
		for key, value := range stored.Defaults {
			if value != "" {
				config.Defaults[key] = value
			}
		}
	}
	return config
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
		response.Protocols = append(response.Protocols, providerRequestProtocol{Protocol: item.key, Name: item.name, BaseURL: base, DefaultBaseURL: config.Defaults[item.key], Supported: item.supported})
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
	db, parent, ok := requestConfigSupplier(w, r)
	if !ok {
		return
	}
	common.ReplyOK(w, requestConfigResponse(db.WithContext(r.Context()), parent))
}

var errUnsupportedRequestModels = errors.New("Anthropic currently supports chat and vision models only; remove incompatible models before changing the default protocol")

func SetProviderRequestConfig(w http.ResponseWriter, r *http.Request) {
	db, parent, ok := requestConfigSupplier(w, r)
	if !ok {
		return
	}
	var req updateProviderRequestConfigRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil {
		common.ReplyErr(w, "invalid request configuration", 400)
		return
	}
	if req.DefaultProtocol != "openai" && req.DefaultProtocol != "anthropic" {
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
		before := requestConfigResponse(tx, parent)
		for _, entry := range before.Protocols {
			if entry.Supported && current.Endpoints[entry.Protocol] == "" {
				current.Endpoints[entry.Protocol] = entry.BaseURL
			}
		}
		for key, value := range req.Endpoints {
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
		encoded, err := json.Marshal(current)
		if err != nil {
			return err
		}
		if len(encoded) > 8192 {
			return credentialvault.ErrInvalidContract
		}
		// Saving an unchanged drawer must not change native provider adapters or
		// invalidate credentials. Persist metadata only when an endpoint changed.
		unchanged := !protocolChanged
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
		if protocolChanged {
			parent.ProtocolName = protocol
			parent.ActiveKeyRevision++
		}
		if err := tx.Model(&parent).Updates(map[string]any{"request_config": parent.RequestConfig, "protocol_name": parent.ProtocolName, "active_key_revision": parent.ActiveKeyRevision, "updated_at": now}).Error; err != nil {
			return err
		}
		if protocolChanged && tx.Migrator().HasTable(&orm.UserModelProviderGroupModel{}) {
			if err := tx.Model(&orm.UserModelProviderGroupModel{}).Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", parent.ID, parent.CreateUserID).Updates(map[string]any{"provider_name": protocol, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return updateProviderEndpointLocked(tx, &parent, effective, now, true, protocolChanged)
	})
	if err != nil {
		if errors.Is(err, errUnsupportedRequestModels) || errors.Is(err, credentialvault.ErrInvalidContract) {
			common.ReplyErr(w, err.Error(), 400)
		} else {
			common.ReplyErr(w, "save request configuration failed", 500)
		}
		return
	}
	common.ReplyOK(w, requestConfigResponse(db.WithContext(r.Context()), parent))
}
