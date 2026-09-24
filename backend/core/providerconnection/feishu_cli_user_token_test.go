package providerconnection

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The algorithm-facing credential contract remains a real user Bearer token.
// This optional backend method lets old scanner-only runtimes fail safely.
type cliUserTokenBackend interface {
	UserAccessToken(context.Context, string, string, string, string) (ResolvedToken, error)
}

type userTokenRegistry struct {
	meta        ConnectionMeta
	legacyCalls int
}

func (r *userTokenRegistry) Connection(context.Context, string, string) (ConnectionMeta, error) {
	return r.meta, nil
}
func (r *userTokenRegistry) LegacyAccessToken(context.Context, string, string) (string, error) {
	r.legacyCalls++
	return "fixture-legacy", nil
}
func (*userTokenRegistry) UpsertManaged(context.Context, ManagedMirror) error { return nil }

type userTokenBackend struct {
	FeishuCLIBackend
	result    ResolvedToken
	err       error
	calls     int
	arguments []string
}

func (b *userTokenBackend) UserAccessToken(ctx context.Context, owner, connection, profile, capability string) (ResolvedToken, error) {
	b.calls++
	b.arguments = []string{owner, connection, profile, capability}
	if err := ctx.Err(); err != nil {
		return ResolvedToken{}, err
	}
	if b.err != nil {
		return ResolvedToken{}, b.err
	}
	result := b.result
	if result.AccessToken == "fixture-access" {
		result.AccessToken = fmt.Sprintf("fixture-access-%d", b.calls)
	}
	return result, nil
}

func newCLIUserTokenFixture(t *testing.T) (*Service, *userTokenRegistry, *userTokenBackend) {
	t.Helper()
	r := &userTokenRegistry{meta: ConnectionMeta{AuthConnectionID: "fixture-connection", OwnerUserID: "fixture-owner", Provider: "feishu", ConnectionMethod: "cli_personal_app", CredentialLocation: "local", ProfileRef: "fixture-profile", Status: "ACTIVE", ProviderOptions: map[string]any{"chat_enabled": true}}}
	b := &userTokenBackend{result: ResolvedToken{AuthConnectionID: r.meta.AuthConnectionID, Provider: "feishu", AccessToken: "fixture-access", TokenType: "Bearer", SubjectType: "user", Status: "ACTIVE"}}
	s, err := NewLocalService(r, mirrorRecoveryAuthorizer{}, "fixture-client-instance")
	if err != nil {
		t.Fatal(err)
	}
	s.FeishuCLI = b
	return s, r, b
}
func cliChatRequest(capability string) ResolveRequest {
	return ResolveRequest{AuthConnectionID: "fixture-connection", UserID: "fixture-owner", SourceID: "chat:feishu", BindingID: "chat:fixture-connection", Consumer: "chat", RequiredCapability: capability}
}

func TestCLIChatAndWriterReceiveBearerWithoutCloud(t *testing.T) {
	for _, capability := range []string{"chat.search", "chat.read", "chat.write"} {
		t.Run(capability, func(t *testing.T) {
			s, registry, backend := newCLIUserTokenFixture(t)
			backend.result.ExpiresAt = time.Now().Add(time.Hour)
			for call := 1; call <= 2; call++ {
				got, err := s.ResolveAccessToken(t.Context(), cliChatRequest(capability))
				if err != nil || got.TokenType != "Bearer" || got.SubjectType != "user" || got.AccessToken != fmt.Sprintf("fixture-access-%d", call) {
					t.Fatal("CLI did not supply a fresh algorithm-compatible user token")
				}
			}
			if backend.calls != 2 || registry.legacyCalls != 0 || strings.Join(backend.arguments, "/") != "fixture-owner/fixture-connection/fixture-profile/"+capability {
				t.Fatal("CLI credential routing changed identity or used a legacy fallback")
			}
		})
	}
}

func TestCLIChatRejectsIneligibleConnectionsBeforeExport(t *testing.T) {
	for _, invalid := range []string{"owner", "connection", "provider", "revoked", "disabled", "unset", "string flag", "profile", "location", "capability", "canceled"} {
		t.Run(invalid, func(t *testing.T) {
			s, registry, backend := newCLIUserTokenFixture(t)
			request := cliChatRequest("chat.search")
			ctx := t.Context()
			switch invalid {
			case "owner":
				registry.meta.OwnerUserID = "fixture-other"
			case "connection":
				registry.meta.AuthConnectionID = "fixture-other"
			case "provider":
				registry.meta.Provider = "notion"
			case "revoked":
				registry.meta.Status = "REVOKED"
			case "disabled":
				registry.meta.ProviderOptions["chat_enabled"] = false
			case "unset":
				registry.meta.ProviderOptions = nil
			case "string flag":
				registry.meta.ProviderOptions["chat_enabled"] = "true"
			case "profile":
				registry.meta.ProfileRef = ""
			case "location":
				registry.meta.CredentialLocation = "cloud"
			case "capability":
				request.RequiredCapability = "chat.unknown"
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			result, err := s.ResolveAccessToken(ctx, request)
			if err == nil || result.AccessToken != "" || backend.calls != 0 || registry.legacyCalls != 0 {
				t.Fatal("ineligible connection exported credentials or a CLI handle")
			}
		})
	}
}

func TestCLIChatRejectsInvalidCredentialResultsWithoutFallback(t *testing.T) {
	for _, invalid := range []string{"backend unavailable", "connection", "provider", "status", "subject", "type", "empty", "opaque", "expired"} {
		t.Run(invalid, func(t *testing.T) {
			s, registry, b := newCLIUserTokenFixture(t)
			switch invalid {
			case "backend unavailable":
				b.err = ErrCLIUnavailable
			case "connection":
				b.result.AuthConnectionID = "fixture-other"
			case "provider":
				b.result.Provider = "notion"
			case "status":
				b.result.Status = "REVOKED"
			case "subject":
				b.result.SubjectType = "tenant"
			case "type":
				b.result.TokenType = "CLIProfile"
			case "empty":
				b.result.AccessToken = " "
			case "opaque":
				b.result.AccessToken = "lmc_fcli_fixture"
			case "expired":
				b.result.ExpiresAt = time.Now().Add(-time.Minute)
			}
			result, err := s.ResolveAccessToken(t.Context(), cliChatRequest("chat.write"))
			if err == nil || result.AccessToken != "" || b.calls != 1 || registry.legacyCalls != 0 {
				t.Fatal("invalid CLI credential escaped validation or used a fallback")
			}
		})
	}
}

func TestCLIScannerKeepsProfileHandleAndNeverExportsBearer(t *testing.T) {
	s, registry, backend := newCLIUserTokenFixture(t)
	registry.meta.ProviderOptions["chat_enabled"] = false
	got, err := s.ResolveAccessToken(t.Context(), ResolveRequest{AuthConnectionID: registry.meta.AuthConnectionID, UserID: registry.meta.OwnerUserID, SourceID: "fixture-source", BindingID: "fixture-binding", Consumer: "datasource", ContextMode: ContextModeSourceBinding, RequiredCapability: "datasource.read"})
	if err != nil || got.TokenType != "CLIProfile" || !strings.HasPrefix(got.AccessToken, "lmc_fcli_") || backend.calls != 0 {
		t.Fatal("scanner credential boundary changed")
	}
}

func TestCLIOlderBackendKeepsScanningButCannotExportChatCredentials(t *testing.T) {
	s, registry, _ := newCLIUserTokenFixture(t)
	// The old interface has no UserAccessToken method; creating read handles
	// must remain possible while chat fails without handing a handle to Python.
	s.FeishuCLI = &struct{ FeishuCLIBackend }{}
	read, err := s.ResolveAccessToken(t.Context(), ResolveRequest{AuthConnectionID: registry.meta.AuthConnectionID, UserID: registry.meta.OwnerUserID, SourceID: "fixture-source", BindingID: "fixture-binding", Consumer: "datasource", ContextMode: ContextModeSourceBinding, RequiredCapability: "datasource.read"})
	if err != nil || read.TokenType != "CLIProfile" {
		t.Fatal("old CLI backend lost scanning support")
	}
	chat, err := s.ResolveAccessToken(t.Context(), cliChatRequest("chat.search"))
	if err == nil || chat.AccessToken != "" {
		t.Fatal("old backend exported an unusable chat credential")
	}
}

// The coordinator must validate the stored profile binding and fresh CLI scope
// checks before accepting the helper's verified user_info result.
type credentialRuntime struct {
	*fakeFeishuCLIRuntime
	identity   FeishuCLIUserIdentity
	token      string
	missing    bool
	partial    bool
	requested  []string
	tokenCalls int
	scopeCalls int
}

func (r *credentialRuntime) AuthCheck(_ context.Context, _ string, scopes []string) (FeishuCLIAuthCheck, error) {
	r.scopeCalls++
	r.requested = append([]string(nil), scopes...)
	if r.missing {
		return FeishuCLIAuthCheck{OK: false, Missing: scopes}, nil
	}
	if r.partial {
		return FeishuCLIAuthCheck{OK: true, Granted: []string{"drive:drive:readonly", "docx:document:readonly"}}, nil
	}
	return FeishuCLIAuthCheck{OK: true, Granted: scopes}, nil
}
func (r *credentialRuntime) UserAccessToken(ctx context.Context, profileDir string) (string, FeishuCLIUserIdentity, error) {
	r.tokenCalls++
	if err := ctx.Err(); err != nil {
		return "", FeishuCLIUserIdentity{}, err
	}
	return r.token, r.identity, nil
}

func TestCLICoordinatorChecksProfileIdentityAndWriteGrants(t *testing.T) {
	for _, mode := range []string{"write", "read", "wrong owner", "wrong connection", "wrong profile", "wrong user", "wrong tenant", "missing grants", "missing read grants", "partial grants", "empty token", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			profiles, err := NewFeishuCLIProfileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			profile, err := profiles.Ensure(t.Context(), "fixture-owner", "fixture-connection")
			if err != nil {
				t.Fatal(err)
			}
			if err := profiles.BindIdentity(t.Context(), profile, "fixture-tenant", "ou_fixture", time.Now()); err != nil {
				t.Fatal(err)
			}
			runtime := &credentialRuntime{fakeFeishuCLIRuntime: newFakeFeishuCLIRuntime(), identity: FeishuCLIUserIdentity{OpenID: "ou_fixture", TenantKey: "fixture-tenant"}, token: "fixture-access"}
			coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, &fakeFeishuCLIConnectionRegistry{}, DefaultFeishuCLIScopes)
			if err != nil {
				t.Fatal(err)
			}
			backend, ok := any(coordinator).(cliUserTokenBackend)
			if !ok {
				t.Fatal("CLI coordinator has no user-token handoff")
			}
			owner, connection, reference, capability := profile.LocalUserID, profile.ConnectionID, profile.Reference, "chat.write"
			ctx := t.Context()
			switch mode {
			case "read":
				capability = "chat.search"
			case "wrong owner":
				owner = "fixture-other"
			case "wrong connection":
				connection = "fixture-other"
			case "wrong profile":
				reference = "fixture-other"
			case "wrong user":
				runtime.identity.OpenID = "ou_other"
			case "wrong tenant":
				runtime.identity.TenantKey = "fixture-other"
			case "missing grants":
				runtime.missing = true
			case "missing read grants":
				capability = "chat.search"
				runtime.missing = true
			case "partial grants":
				runtime.partial = true
			case "empty token":
				runtime.token = ""
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			result, err := backend.UserAccessToken(ctx, owner, connection, reference, capability)
			if mode != "read" && mode != "write" {
				if err == nil || result.AccessToken != "" {
					t.Fatal("unverified profile or scope exported a token")
				}
				if (strings.HasPrefix(mode, "wrong ") && mode != "wrong user" && mode != "wrong tenant" || mode == "missing grants" || mode == "missing read grants" || mode == "partial grants" || mode == "canceled") && runtime.tokenCalls != 0 {
					t.Fatal("rejected request invoked the credential helper")
				}
				return
			}
			if err != nil || result.AccessToken != "fixture-access" || result.TokenType != "Bearer" || result.SubjectType != "user" || result.AuthConnectionID != connection || result.Provider != "feishu" || result.Status != "ACTIVE" {
				t.Fatal("verified CLI credential did not satisfy the existing consumer contract")
			}
			grants := " " + strings.Join(runtime.requested, " ") + " "
			for _, scope := range []string{"offline_access", "drive:drive:readonly", "wiki:space:retrieve", "wiki:node:read", "wiki:node:retrieve", "docx:document:readonly"} {
				if !strings.Contains(grants, " "+scope+" ") {
					t.Fatal("credential handoff omitted required reading scopes")
				}
			}
			for _, scope := range []string{"drive:drive", "wiki:wiki", "docx:document"} {
				if strings.Contains(grants, " "+scope+" ") != (mode == "write") {
					t.Fatal("requested scopes did not distinguish reading and writing")
				}
			}
			if len(runtime.requested) == 0 || runtime.tokenCalls != 1 {
				t.Fatal("fresh CLI authorization was not checked")
			}
			runtime.token = "fixture-rotated-access"
			rotated, err := backend.UserAccessToken(ctx, owner, connection, reference, capability)
			if err != nil || rotated.AccessToken != runtime.token || runtime.tokenCalls != 2 || runtime.scopeCalls != 2 {
				t.Fatal("coordinator reused credentials or skipped a fresh grant check")
			}
			runtime.missing = true
			revoked, err := backend.UserAccessToken(ctx, owner, connection, reference, capability)
			if err == nil || revoked.AccessToken != "" || runtime.scopeCalls != 4 || runtime.tokenCalls != 2 {
				t.Fatal("coordinator exported a token after grants were revoked")
			}
		})
	}
}
