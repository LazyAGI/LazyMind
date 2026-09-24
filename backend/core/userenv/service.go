package userenv

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
)

var (
	userEnvNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	userEnvControlPattern = regexp.MustCompile(`(?i)^(LD_|DYLD_)`)
	userEnvBlockedNames   = map[string]struct{}{
		"HOME": {}, "PATH": {}, "PYTHONPATH": {}, "PYTHONHOME": {}, "PYTHONSTARTUP": {},
		"PYTHONEXECUTABLE": {}, "LD_LIBRARY_PATH": {}, "LD_PRELOAD": {}, "DYLD_LIBRARY_PATH": {},
		"DYLD_INSERT_LIBRARIES": {}, "SHELL": {}, "PWD": {}, "IFS": {}, "ENV": {}, "BASH_ENV": {},
		"HTTP_PROXY": {}, "HTTPS_PROXY": {}, "ALL_PROXY": {}, "NO_PROXY": {}, "FTP_PROXY": {},
		"SSL_CERT_FILE": {}, "SSL_CERT_DIR": {}, "REQUESTS_CA_BUNDLE": {}, "CURL_CA_BUNDLE": {},
		"SSLKEYLOGFILE": {}, "NODE_OPTIONS": {}, "NODE_EXTRA_CA_CERTS": {},
		"RUBYOPT": {}, "RUBYLIB": {}, "PERL5OPT": {}, "PERL5LIB": {},
		"GIT_CONFIG": {}, "GIT_CONFIG_GLOBAL": {}, "GIT_CONFIG_SYSTEM": {},
		"GIT_CONFIG_COUNT": {}, "GIT_SSH_COMMAND": {}, "ZDOTDIR": {}, "PROMPT_COMMAND": {},
		"PYTHONINSPECT": {}, "PYTHONBREAKPOINT": {}, "NODE_PATH": {}, "NODE_TLS_REJECT_UNAUTHORIZED": {},
		"OPENSSL_CONF": {}, "OPENSSL_MODULES": {}, "GIT_CONFIG_PARAMETERS": {},
		"GIT_SSL_NO_VERIFY": {}, "GIT_SSL_CAINFO": {}, "GIT_SSL_CAPATH": {},
	}
)

type Variable struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Enabled          bool      `json:"enabled"`
	Description      string    `json:"description"`
	MaskedValue      string    `json:"masked_value"`
	CredentialStatus string    `json:"credential_status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type CreateRequest struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Enabled     *bool  `json:"enabled"`
	Description string `json:"description"`
}

type PatchRequest struct {
	Name              *string    `json:"name"`
	Value             *string    `json:"value"`
	Enabled           *bool      `json:"enabled"`
	Description       *string    `json:"description"`
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at"`
}

func NormalizeName(name string) (string, error) {
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
	return cleaned, nil
}

func MaskValue(value string) string {
	runes := []rune(value)
	if len(runes) <= 16 {
		return "••••••••"
	}
	prefix, suffix := 4, 4
	return string(runes[:prefix]) + "****" + string(runes[len(runes)-suffix:])
}

func responseFromValue(row orm.UserEnvironmentVariable, value string) Variable {
	status := "available"
	if _, err := NormalizeName(row.Name); err != nil {
		status = "invalid_name"
	}
	return Variable{
		ID:               row.ID,
		Name:             row.Name,
		Enabled:          row.Enabled,
		Description:      row.Description,
		MaskedValue:      MaskValue(value),
		CredentialStatus: status,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}

func List(db *gorm.DB, userID string) ([]Variable, error) {
	var rows []orm.UserEnvironmentVariable
	if err := db.Where("user_id = ?", userID).Order("updated_at DESC").Find(&rows).Error; err != nil {
		return nil, common.NewAppError(http.StatusInternalServerError, common.ErrCodeInternal, "Failed to query user environment variables")
	}
	items := make([]Variable, 0, len(rows))
	for _, row := range rows {
		value, err := UpgradeCredential(db, &row)
		item := responseFromValue(row, value)
		if err != nil {
			log.Logger.Error().Err(err).Str("user_id", userID).Str("env_id", row.ID).Msg("decrypt user env var failed")
			if item.CredentialStatus == "available" {
				item.CredentialStatus = "unavailable"
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// Delete accepts an existing transaction so confirmation consumption and deletion commit together.
func Delete(db *gorm.DB, userID, id string, name string, expected *time.Time) error {
	query := db.Where("id = ? AND user_id = ?", id, userID)
	if expected != nil {
		query = query.Where("name = ? AND updated_at = ?", name, *expected)
	}
	result := query.Delete(&orm.UserEnvironmentVariable{})
	if result.Error != nil {
		return common.ResolveAppError("delete user env var failed", http.StatusInternalServerError)
	}
	if result.RowsAffected != 1 {
		if expected != nil {
			return ErrConflict
		}
		return common.ResolveAppError("env var not found", http.StatusNotFound)
	}
	return nil
}

func IsDuplicate(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(err.Error(), "idx_user_env_user_name_active") || strings.Contains(err.Error(), "UNIQUE constraint failed: user_environment_variables.user_id, user_environment_variables.name")
}

func Create(db *gorm.DB, userID string, req CreateRequest) (Variable, error) {
	name, err := NormalizeName(req.Name)
	if err != nil {
		return Variable{}, common.ResolveAppError(err.Error(), http.StatusBadRequest)
	}
	value := req.Value
	if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) || len([]rune(req.Description)) > 512 {
		return Variable{}, common.ResolveAppError("invalid environment variable value or description", http.StatusBadRequest)
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	var count int64
	if err := db.Model(&orm.UserEnvironmentVariable{}).
		Where("user_id = ? AND name = ?", userID, name).
		Count(&count).Error; err != nil {
		return Variable{}, common.ResolveAppError("check user env var failed", http.StatusInternalServerError)
	}
	if count > 0 {
		return Variable{}, common.ResolveAppError("env name already exists", http.StatusConflict)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	row := orm.UserEnvironmentVariable{
		ID:                 "env_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		UserID:             userID,
		Name:               name,
		CredentialVersion:  CredentialVersion,
		CredentialRevision: 1,
		Enabled:            enabled,
		Description:        strings.TrimSpace(req.Description),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	ciphertext, err := EncryptValue(row, value)
	if err != nil {
		log.Logger.Error().Err(err).Str("user_id", userID).Msg("encrypt user env var failed")
		return Variable{}, err
	}
	row.ValueCiphertext = ciphertext
	if err := db.Create(&row).Error; err != nil {
		if IsDuplicate(err) {
			return Variable{}, common.ResolveAppError("env name already exists", http.StatusConflict)
		}
		log.Logger.Error().Err(err).Str("user_id", userID).Str("name", name).Msg("create user env var failed")
		return Variable{}, common.ResolveAppError("create user env var failed", http.StatusInternalServerError)
	}
	return responseFromValue(row, value), nil
}

func Patch(db *gorm.DB, userID, id string, req PatchRequest) (Variable, error) {
	var row orm.UserEnvironmentVariable
	if err := db.Where("id = ? AND user_id = ?", id, userID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Variable{}, common.ResolveAppError("env var not found", http.StatusNotFound)
		}
		return Variable{}, common.NewAppError(
			http.StatusInternalServerError,
			common.ErrCodeInternal,
			"Failed to query user environment variable",
		)
	}
	if req.Description != nil && len([]rune(*req.Description)) > 512 {
		return Variable{}, common.ResolveAppError("environment variable description is too long", http.StatusBadRequest)
	}
	// PostgreSQL stores timestamps at microsecond precision; older responses may include nanoseconds.
	if req.ExpectedUpdatedAt != nil && req.ExpectedUpdatedAt.UnixMicro() != row.UpdatedAt.UnixMicro() {
		return Variable{}, ErrConflict
	}
	if req.Value != nil && (strings.TrimSpace(*req.Value) == "" || strings.ContainsRune(*req.Value, 0)) {
		return Variable{}, common.ResolveAppError("invalid environment variable value", http.StatusBadRequest)
	}
	if req.Enabled != nil && *req.Enabled {
		name := row.Name
		if req.Name != nil {
			name = *req.Name
		}
		if _, err := NormalizeName(name); err != nil {
			return Variable{}, common.ResolveAppError(err.Error(), http.StatusBadRequest)
		}
	}
	displayValue := ""
	credentialAvailable := true
	if req.Value == nil {
		var err error
		displayValue, err = UpgradeCredential(db, &row)
		if err != nil {
			if errors.Is(err, ErrConflict) ||
				(req.Enabled != nil && *req.Enabled) ||
				(req.Name != nil && strings.TrimSpace(*req.Name) != row.Name) {
				return Variable{}, err
			}
			credentialAvailable = false
		}
	}
	originalRow := row
	updates := map[string]any{}
	nameChanged := false
	if req.Name != nil {
		name, err := NormalizeName(*req.Name)
		if err != nil {
			return Variable{}, common.ResolveAppError(err.Error(), http.StatusBadRequest)
		}
		if name != row.Name {
			var count int64
			if err := db.Model(&orm.UserEnvironmentVariable{}).
				Where("user_id = ? AND name = ? AND id <> ?", userID, name, row.ID).
				Count(&count).Error; err != nil {
				return Variable{}, common.ResolveAppError("check user env var failed", http.StatusInternalServerError)
			}
			if count > 0 {
				return Variable{}, common.ResolveAppError("env name already exists", http.StatusConflict)
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
		row.CredentialVersion = CredentialVersion
		row.CredentialRevision++
		ciphertext, err := EncryptValue(row, displayValue)
		if err != nil {
			log.Logger.Error().Err(err).Str("user_id", userID).Str("env_id", row.ID).Msg("encrypt user env var failed")
			return Variable{}, err
		}
		row.ValueCiphertext = ciphertext
		updates["value_ciphertext"] = ciphertext
		updates["credential_revision"] = row.CredentialRevision
		updates["credential_version"] = CredentialVersion
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
	tx := RevisionQuery(db, originalRow).Updates(updates)
	if tx.Error != nil {
		return Variable{}, tx.Error
	}
	if tx.RowsAffected != 1 {
		return Variable{}, ErrConflict
	}
	response := responseFromValue(row, displayValue)
	if !credentialAvailable && response.CredentialStatus == "available" {
		response.CredentialStatus = "unavailable"
	}
	return response, nil
}
