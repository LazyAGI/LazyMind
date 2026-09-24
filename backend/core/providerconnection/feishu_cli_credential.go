package providerconnection

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

// ConfigureCredentialHelper is called during startup. Failure leaves ordinary
// CLI login/scanning available, but disables algorithm-facing token export.
func (runner *FeishuCLIRunner) ConfigureCredentialHelper(path, checksum string) error {
	helper, err := NewFeishuCLIRunner(path, checksum)
	runner.credentialHelper = helper
	return err
}

func (runner *FeishuCLIRunner) UserAccessToken(ctx context.Context, profileDir string) (string, FeishuCLIUserIdentity, error) {
	if err := ctx.Err(); err != nil {
		return "", FeishuCLIUserIdentity{}, err
	}
	if runner == nil || runner.credentialHelper == nil || !validFeishuCLIProfileDirectory(profileDir) {
		return "", FeishuCLIUserIdentity{}, ErrCLINotInstalled
	}
	if err := prepareFeishuCLIProfileDataDirectory(profileDir); err != nil {
		return "", FeishuCLIUserIdentity{}, err
	}
	// Keep the child cwd and its isolated configuration paths identical on
	// hosts such as macOS where /var is a symlink to /private/var.
	resolvedDir, err := filepath.EvalSymlinks(profileDir)
	if err != nil {
		return "", FeishuCLIUserIdentity{}, ErrCLIProfileNotFound
	}
	profileDir = resolvedDir
	commandContext, cancel := context.WithTimeout(ctx, runner.commandTimeout)
	defer cancel()
	command := exec.CommandContext(commandContext, runner.credentialHelper.binaryPath)
	command.Dir = profileDir
	for _, item := range runner.environment(profileDir) {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToUpper(key) {
		case "HOME", "PATH", "TMPDIR", "TMP", "TEMP", "SYSTEMROOT", "WINDIR", "COMSPEC", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
			"SSL_CERT_FILE", "SSL_CERT_DIR", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "DBUS_SESSION_BUS_ADDRESS",
			"LARKSUITE_CLI_CONFIG_DIR", "LARKSUITE_CLI_DATA_DIR", "LARKSUITE_CLI_NO_UPDATE_NOTIFIER", "LARKSUITE_CLI_NO_SKILLS_NOTIFIER":
			command.Env = append(command.Env, item)
		}
	}
	stdout, stderr := &boundedBuffer{limit: 16 << 10}, &boundedBuffer{limit: 16 << 10}
	command.Stdout, command.Stderr = stdout, stderr
	err = command.Run()
	if err = classifyFeishuCLIProcessError(commandContext, err, stdout.overflowed() || stderr.overflowed()); err != nil {
		return "", FeishuCLIUserIdentity{}, err
	}
	var credential struct {
		AccessToken string `json:"access_token"`
		OpenID      string `json:"open_id"`
		TenantKey   string `json:"tenant_key"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&credential) != nil || decoder.Decode(&struct{}{}) != io.EOF || !validFeishuCLIUserToken(credential.AccessToken) ||
		strings.TrimSpace(credential.OpenID) == "" || strings.TrimSpace(credential.TenantKey) == "" || len(credential.OpenID) > 512 || len(credential.TenantKey) > 512 {
		return "", FeishuCLIUserIdentity{}, ErrCLIOutputInvalid
	}
	return credential.AccessToken, FeishuCLIUserIdentity{OpenID: credential.OpenID, TenantKey: credential.TenantKey}, nil
}

func validFeishuCLIUserToken(token string) bool {
	if token == "" || len(token) > 8192 || strings.HasPrefix(token, "lmc_fcli_") {
		return false
	}
	for _, character := range token {
		if character <= ' ' || character >= 127 {
			return false
		}
	}
	return true
}

func feishuCLIToolScopes(capability string) ([]string, error) {
	switch capability {
	case "chat.read", "chat.search":
		return append([]string(nil), feishuCLIReadScopes...), nil
	case "chat.write":
		return append([]string(nil), DefaultFeishuCLIReadScopes...), nil
	default:
		return nil, ErrCLIUnavailable
	}
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) UserAccessToken(ctx context.Context, owner, connection, reference, capability string) (ResolvedToken, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedToken{}, err
	}
	scopes, err := feishuCLIToolScopes(capability)
	if err != nil {
		return ResolvedToken{}, err
	}
	profile, err := coordinator.profiles.Load(ctx, owner, connection)
	if err != nil {
		return ResolvedToken{}, err
	}
	if profile.Reference != reference {
		return ResolvedToken{}, ErrCLIProfileOwnerMismatch
	}
	bound, err := coordinator.profiles.Identity(ctx, profile)
	if err != nil || bound.LocalUserID != owner || bound.ConnectionID != connection {
		return ResolvedToken{}, ErrCLIProfileOwnerMismatch
	}
	runner, ok := coordinator.runner.(interface {
		UserAccessToken(context.Context, string) (string, FeishuCLIUserIdentity, error)
	})
	if !ok {
		return ResolvedToken{}, ErrCLINotInstalled
	}
	checked, err := coordinator.runner.AuthCheck(ctx, profile.ConfigDir, scopes)
	if err != nil {
		return ResolvedToken{}, err
	}
	granted := make(map[string]bool, len(checked.Granted))
	for _, scope := range checked.Granted {
		granted[scope] = true
	}
	if !checked.OK || len(checked.Missing) != 0 {
		return ResolvedToken{}, &FeishuCLICommandError{Code: "AUTH_SCOPE_MISSING"}
	}
	for _, scope := range scopes {
		if !granted[scope] {
			return ResolvedToken{}, &FeishuCLICommandError{Code: "AUTH_SCOPE_MISSING"}
		}
	}
	token, identity, err := runner.UserAccessToken(ctx, profile.ConfigDir)
	if err != nil {
		return ResolvedToken{}, err
	}
	if identity.OpenID != bound.OpenID || identity.TenantKey != bound.TenantKey {
		return ResolvedToken{}, ErrCLIProfileTenantMismatch
	}
	if !validFeishuCLIUserToken(token) {
		return ResolvedToken{}, ErrCLIOutputInvalid
	}
	if err := ctx.Err(); err != nil {
		return ResolvedToken{}, err
	}
	// Do not invent a lease expiry or cache this token: the official CLI owns
	// its validity and renewal, and verifies it for each handoff.
	return ResolvedToken{AuthConnectionID: connection, Provider: "feishu", AccessToken: token, TokenType: "Bearer", SubjectType: "user", Status: "ACTIVE"}, nil
}

func (service *Service) resolveFeishuCLIToolToken(ctx context.Context, meta ConnectionMeta, request ResolveRequest) (ResolvedToken, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedToken{}, err
	}
	if _, err := feishuCLIToolScopes(request.RequiredCapability); err != nil {
		return ResolvedToken{}, err
	}
	if meta.Status != "ACTIVE" || meta.ProviderOptions["chat_enabled"] != true {
		return ResolvedToken{}, ErrCLIUnavailable
	}
	backend, ok := service.FeishuCLI.(interface {
		UserAccessToken(context.Context, string, string, string, string) (ResolvedToken, error)
	})
	if !ok {
		return ResolvedToken{}, ErrCLINotInstalled
	}
	result, err := backend.UserAccessToken(ctx, request.UserID, meta.AuthConnectionID, meta.ProfileRef, request.RequiredCapability)
	if err != nil {
		return ResolvedToken{}, err
	}
	if ctx.Err() != nil {
		return ResolvedToken{}, ctx.Err()
	}
	if result.AuthConnectionID != meta.AuthConnectionID || result.Provider != "feishu" || result.Status != "ACTIVE" || result.SubjectType != "user" || result.TokenType != "Bearer" ||
		!validFeishuCLIUserToken(result.AccessToken) || (!result.ExpiresAt.IsZero() && !result.ExpiresAt.After(service.now())) {
		return ResolvedToken{}, ErrCLIOutputInvalid
	}
	return result, nil
}
