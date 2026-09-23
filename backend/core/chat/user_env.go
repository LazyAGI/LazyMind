package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"
	"lazymind/core/userenv"
)

var (
	userEnvNamePattern       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	userEnvCredentialPattern = regexp.MustCompile(`(?i)(^|_)(API_)?(KEY|TOKEN|SECRET|PASSWORD|PASS|CREDENTIAL|CREDENTIALS|AUTH|ACCESS|REFRESH)(_|$)|(_API_KEY$)`)
	userEnvControlPattern    = regexp.MustCompile(`(?i)(^|_)(PATH|HOME|SHELL|ENV|PROXY|PRELOAD|LIBRARY|CERT|BUNDLE|OPTIONS?|OPTS|CONFIG|RC|PROFILE|STARTUP)(_|$)`)
	userEnvBlockedNames      = map[string]struct{}{
		"HOME": {}, "PATH": {}, "PYTHONPATH": {}, "PYTHONHOME": {}, "PYTHONSTARTUP": {},
		"PYTHONEXECUTABLE": {}, "LD_LIBRARY_PATH": {}, "LD_PRELOAD": {}, "DYLD_LIBRARY_PATH": {},
		"DYLD_INSERT_LIBRARIES": {}, "SHELL": {}, "PWD": {}, "IFS": {}, "ENV": {}, "BASH_ENV": {},
		"HTTP_PROXY": {}, "HTTPS_PROXY": {}, "ALL_PROXY": {}, "NO_PROXY": {}, "FTP_PROXY": {},
		"SSL_CERT_FILE": {}, "SSL_CERT_DIR": {}, "REQUESTS_CA_BUNDLE": {}, "CURL_CA_BUNDLE": {},
		"SSLKEYLOGFILE": {},
	}
)

type userEnvVariableResponse struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Enabled          bool      `json:"enabled"`
	Description      string    `json:"description"`
	MaskedValue      string    `json:"masked_value"`
	CredentialStatus string    `json:"credential_status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type listUserEnvVariablesResponse struct {
	Items []userEnvVariableResponse `json:"items"`
}

type createUserEnvVariableRequest struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Enabled     *bool  `json:"enabled"`
	Description string `json:"description"`
}

type patchUserEnvVariableRequest struct {
	Name              *string    `json:"name"`
	Value             *string    `json:"value"`
	Enabled           *bool      `json:"enabled"`
	Description       *string    `json:"description"`
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at"`
}

func normalizeUserEnvName(name string) (string, error) {
	cleaned := strings.TrimSpace(name)
	if cleaned == "" {
		return "", fmt.Errorf("env name is required")
	}
	if len(cleaned) > 128 || !userEnvNamePattern.MatchString(cleaned) {
		return "", fmt.Errorf("env name must match ^[A-Za-z_][A-Za-z0-9_]*$")
	}
	normalized := strings.ToUpper(cleaned)
	if _, blocked := userEnvBlockedNames[normalized]; blocked {
		return "", fmt.Errorf("env name %q is reserved", normalized)
	}
	if userEnvControlPattern.MatchString(normalized) {
		return "", fmt.Errorf("env name %q controls runtime behavior", normalized)
	}
	if !userEnvCredentialPattern.MatchString(normalized) {
		return "", fmt.Errorf("env name must look like a credential name, such as *_API_KEY, *_TOKEN, or *_SECRET")
	}
	return cleaned, nil
}

func maskUserEnvValue(value string) string {
	runes := []rune(value)
	if len(runes) <= 16 {
		return "••••••••"
	}
	prefix, suffix := 4, 4
	return string(runes[:prefix]) + "****" + string(runes[len(runes)-suffix:])
}

func userEnvResponseFromValue(row orm.UserEnvironmentVariable, value string) userEnvVariableResponse {
	return userEnvVariableResponse{
		ID:               row.ID,
		Name:             row.Name,
		Enabled:          row.Enabled,
		Description:      row.Description,
		MaskedValue:      maskUserEnvValue(value),
		CredentialStatus: "available",
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}

func userEnvRequestUserID(r *http.Request) string {
	return strings.TrimSpace(store.UserID(r))
}

func ensureUserEnvUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := userEnvRequestUserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing user id", http.StatusUnauthorized)
		return "", false
	}
	return userID, true
}

func userEnvID(r *http.Request) string {
	return strings.TrimSpace(mux.Vars(r)["id"])
}

func ListUserEnvironmentVariables(w http.ResponseWriter, r *http.Request) {
	userID, ok := ensureUserEnvUserID(w, r)
	if !ok {
		return
	}
	var rows []orm.UserEnvironmentVariable
	if err := store.DB().WithContext(r.Context()).
		Where("user_id = ?", userID).
		Order("updated_at DESC").
		Find(&rows).Error; err != nil {
		common.ReplyAppErr(w, common.NewAppError(
			http.StatusInternalServerError,
			common.ErrCodeInternal,
			"Failed to query user environment variables",
		))
		return
	}
	items := make([]userEnvVariableResponse, 0, len(rows))
	for _, row := range rows {
		value, err := userenv.UpgradeCredential(store.DB().WithContext(r.Context()), &row)
		if err != nil {
			log.Logger.Error().Err(err).Str("user_id", userID).Str("env_id", row.ID).Msg("decrypt user env var failed")
			item := userEnvResponseFromValue(row, "")
			item.CredentialStatus = "unavailable"
			items = append(items, item)
			continue
		}
		items = append(items, userEnvResponseFromValue(row, value))
	}
	common.ReplyOK(w, listUserEnvVariablesResponse{Items: items})
}

func CreateUserEnvironmentVariable(w http.ResponseWriter, r *http.Request) {
	userID, ok := ensureUserEnvUserID(w, r)
	if !ok {
		return
	}
	var req createUserEnvVariableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid json", http.StatusBadRequest)
		return
	}
	name, err := normalizeUserEnvName(req.Name)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	value := req.Value
	if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) || len([]rune(req.Description)) > 512 {
		common.ReplyErr(w, "invalid environment variable value or description", http.StatusBadRequest)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	db := store.DB()
	var count int64
	if err := db.WithContext(r.Context()).Model(&orm.UserEnvironmentVariable{}).
		Where("user_id = ? AND name = ?", userID, name).
		Count(&count).Error; err != nil {
		common.ReplyErr(w, "check user env var failed", http.StatusInternalServerError)
		return
	}
	if count > 0 {
		common.ReplyErr(w, "env name already exists", http.StatusConflict)
		return
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	row := orm.UserEnvironmentVariable{
		ID:                 "env_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		UserID:             userID,
		Name:               name,
		CredentialVersion:  userenv.CredentialVersion,
		CredentialRevision: 1,
		Enabled:            enabled,
		Description:        strings.TrimSpace(req.Description),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	ciphertext, err := userenv.EncryptValue(row, value)
	if err != nil {
		log.Logger.Error().Err(err).Str("user_id", userID).Msg("encrypt user env var failed")
		replyUserEnvError(w, err)
		return
	}
	row.ValueCiphertext = ciphertext
	if err := db.WithContext(r.Context()).Create(&row).Error; err != nil {
		if isUserEnvDuplicate(err) {
			common.ReplyErr(w, "env name already exists", http.StatusConflict)
			return
		}
		log.Logger.Error().Err(err).Str("user_id", userID).Str("name", name).Msg("create user env var failed")
		common.ReplyErr(w, "create user env var failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, userEnvResponseFromValue(row, value))
}

func PatchUserEnvironmentVariable(w http.ResponseWriter, r *http.Request) {
	userID, ok := ensureUserEnvUserID(w, r)
	if !ok {
		return
	}
	id := userEnvID(r)
	if id == "" {
		common.ReplyErr(w, "missing env id", http.StatusBadRequest)
		return
	}
	var req patchUserEnvVariableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid json", http.StatusBadRequest)
		return
	}
	db := store.DB()
	var row orm.UserEnvironmentVariable
	if err := db.WithContext(r.Context()).Where("id = ? AND user_id = ?", id, userID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "env var not found", http.StatusNotFound)
			return
		}
		common.ReplyAppErr(w, common.NewAppError(
			http.StatusInternalServerError,
			common.ErrCodeInternal,
			"Failed to query user environment variable",
		))
		return
	}
	if req.Description != nil && len([]rune(*req.Description)) > 512 {
		common.ReplyErr(w, "environment variable description is too long", http.StatusBadRequest)
		return
	}
	// PostgreSQL stores timestamps at microsecond precision; older responses may include nanoseconds.
	if req.ExpectedUpdatedAt != nil && req.ExpectedUpdatedAt.UnixMicro() != row.UpdatedAt.UnixMicro() {
		replyUserEnvError(w, userenv.ErrConflict)
		return
	}
	if req.Value != nil && (strings.TrimSpace(*req.Value) == "" || strings.ContainsRune(*req.Value, 0)) {
		common.ReplyErr(w, "invalid environment variable value", http.StatusBadRequest)
		return
	}
	displayValue := ""
	credentialAvailable := true
	if req.Value == nil {
		var err error
		displayValue, err = userenv.UpgradeCredential(db.WithContext(r.Context()), &row)
		if err != nil {
			if errors.Is(err, userenv.ErrConflict) ||
				(req.Enabled != nil && *req.Enabled) ||
				(req.Name != nil && strings.TrimSpace(*req.Name) != row.Name) {
				replyUserEnvError(w, err)
				return
			}
			credentialAvailable = false
		}
	}
	originalRow := row
	updates := map[string]any{}
	nameChanged := false
	if req.Name != nil {
		name, err := normalizeUserEnvName(*req.Name)
		if err != nil {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if name != row.Name {
			var count int64
			if err := db.WithContext(r.Context()).Model(&orm.UserEnvironmentVariable{}).
				Where("user_id = ? AND name = ? AND id <> ?", userID, name, row.ID).
				Count(&count).Error; err != nil {
				common.ReplyErr(w, "check user env var failed", http.StatusInternalServerError)
				return
			}
			if count > 0 {
				common.ReplyErr(w, "env name already exists", http.StatusConflict)
				return
			}
			row.Name = name
			updates["name"] = name
			nameChanged = true
		}
	}
	if req.Value != nil || nameChanged {
		if req.Value != nil {
			displayValue = *req.Value
		}
		row.CredentialVersion = userenv.CredentialVersion
		row.CredentialRevision++
		ciphertext, err := userenv.EncryptValue(row, displayValue)
		if err != nil {
			log.Logger.Error().Err(err).Str("user_id", userID).Str("env_id", row.ID).Msg("encrypt user env var failed")
			replyUserEnvError(w, err)
			return
		}
		row.ValueCiphertext = ciphertext
		updates["value_ciphertext"] = ciphertext
		updates["credential_revision"] = row.CredentialRevision
		updates["credential_version"] = userenv.CredentialVersion
	}
	if req.Enabled != nil {
		row.Enabled = *req.Enabled
		updates["enabled"] = row.Enabled
	}
	if req.Description != nil {
		row.Description = strings.TrimSpace(*req.Description)
		updates["description"] = row.Description
	}
	row.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
	updates["updated_at"] = row.UpdatedAt
	tx := userenv.RevisionQuery(db.WithContext(r.Context()), originalRow).Updates(updates)
	if tx.Error != nil {
		replyUserEnvError(w, tx.Error)
		return
	}
	if tx.RowsAffected != 1 {
		replyUserEnvError(w, userenv.ErrConflict)
		return
	}
	response := userEnvResponseFromValue(row, displayValue)
	if !credentialAvailable {
		response.CredentialStatus = "unavailable"
	}
	common.ReplyOK(w, response)
}

func DeleteUserEnvironmentVariable(w http.ResponseWriter, r *http.Request) {
	userID, ok := ensureUserEnvUserID(w, r)
	if !ok {
		return
	}
	id := userEnvID(r)
	if id == "" {
		common.ReplyErr(w, "missing env id", http.StatusBadRequest)
		return
	}
	tx := store.DB().WithContext(r.Context()).Where("id = ? AND user_id = ?", id, userID).Delete(&orm.UserEnvironmentVariable{})
	if tx.Error != nil {
		common.ReplyErr(w, "delete user env var failed", http.StatusInternalServerError)
		return
	}
	if tx.RowsAffected == 0 {
		common.ReplyErr(w, "env var not found", http.StatusNotFound)
		return
	}
	common.ReplyOK(w, map[string]any{"deleted": true})
}

func applyUserEnvironmentRuntimeConfig(ctx context.Context, db *gorm.DB, userID string, body map[string]any) error {
	env, err := userenv.LoadEnabled(ctx, db, userID)
	if err != nil {
		return err
	}
	body["user_env_vars"] = env
	return nil
}

func isUserEnvDuplicate(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(err.Error(), "idx_user_env_user_name_active") || strings.Contains(err.Error(), "UNIQUE constraint failed: user_environment_variables.user_id, user_environment_variables.name")
}

func replyUserEnvError(w http.ResponseWriter, err error) {
	if errors.Is(err, userenv.ErrConflict) || isUserEnvDuplicate(err) {
		common.ReplyAppErr(w, common.NewAppError(http.StatusConflict, common.ErrCodeConflict, "Environment variable changed or name already exists; reload and retry"))
		return
	}
	common.ReplyAppErr(w, common.NewAppError(http.StatusInternalServerError, common.ErrCodeInternal, "Unable to access user environment variables; check the credential key configuration").WithDetail(map[string]string{
		"reason": "user_env_unavailable",
	}))
}
