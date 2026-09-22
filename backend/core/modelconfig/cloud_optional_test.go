package modelconfig

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"lazymind/core/cloudclient"
	"lazymind/core/cloudsession"
	"lazymind/core/common/orm"
	"lazymind/core/common/secretcrypto"
	"lazymind/core/modelprovider"
)

type optionalCloudTransport func(*http.Request) (*http.Response, error)

func (f optionalCloudTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type rejectedOptionalCloudAuth struct{}

func (rejectedOptionalCloudAuth) Refresh(context.Context, cloudsession.RefreshToken) (cloudsession.TokenPair, error) {
	return cloudsession.TokenPair{}, &cloudclient.CloudError{HTTPStatus: http.StatusUnauthorized}
}

func TestLocalLLMConfigReadsExistingCiphertextWithoutCloudOrKeychain(t *testing.T) {
	t.Setenv("LAZYMIND_CLOUD_BASE_URL", "")
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "existing-local-key")
	restore := modelprovider.SetCredentialKeyManager(nil)
	t.Cleanup(restore)
	runtimeProviderState.RLock()
	previous := runtimeProviderState.provider
	runtimeProviderState.RUnlock()
	SetRuntimeProvider(nil)
	t.Cleanup(func() { SetRuntimeProvider(previous) })
	db := orm.MigrateTestDB(t, &orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{})
	ciphertext, err := secretcrypto.EncodeAESGCM([]byte("personal-key"), "existing-local-key")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	group := orm.UserModelProviderGroup{ID: "personal-group", UserModelProviderID: "personal-provider", Name: "Personal", BaseURL: "https://personal.example/v1", APIKeyCiphertext: string(ciphertext), CredentialVersion: 1, IsVerified: true, BaseModel: orm.BaseModel{CreateUserID: "local-user", CreatedAt: now, UpdatedAt: now}}
	model := orm.UserModelProviderGroupModel{ID: "personal-model", UserModelProviderID: "personal-provider", UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: "personal-chat", ModelType: "llm", BaseModel: group.BaseModel}
	selection := orm.UserSelectedModel{UserID: "local-user", ModelKey: "llm", UserModelProviderGroupModelID: model.ID, CreatedAt: now, UpdatedAt: now}
	for _, row := range []any{&group, &model, &selection} {
		if err := db.DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	config, err := LoadLLMConfig(context.Background(), db.DB, "local-user")
	if err != nil {
		t.Fatal(err)
	}
	llm, ok := config["llm"].(map[string]any)
	if !ok || llm["model"] != "personal-chat" || llm["api_key"] != "personal-key" {
		t.Fatal("local Chat model credentials changed without Cloud")
	}
}

func TestLocalModelsRemainIndependentOfCloudAvailability(t *testing.T) {
	for _, scenario := range []string{"unconfigured", "signed_out", "unreachable", "reauth_required"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			calls := 0
			client, err := cloudclient.New("https://cloud.example", &http.Client{Transport: optionalCloudTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("Cloud must not be contacted")
			})})
			if err != nil {
				t.Fatal(err)
			}
			store := cloudsession.NewMemorySecureTokenStore()
			session := cloudsession.NewService(cloudsession.ServiceDeps{Store: store, Auth: rejectedOptionalCloudAuth{}})
			switch scenario {
			case "unconfigured":
				session = cloudsession.NewService(cloudsession.ServiceDeps{})
			case "signed_out":
				session.SetReachability(cloudsession.ReachabilityReachable)
			case "unreachable":
				if err := session.Establish(ctx, cloudsession.TokenPair{AccessToken: "access", RefreshToken: "saved-refresh", AccessExpiresAt: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
				session.SetReachability(cloudsession.ReachabilityUnreachable)
			case "reauth_required":
				if err := store.Save(ctx, "saved-refresh"); err != nil {
					t.Fatal(err)
				}
				if err := session.Restore(ctx); err == nil {
					t.Fatal("expected rejected Cloud credentials")
				}
			}
			provider := &CloudRuntimeProvider{Session: session, Client: client}
			config, available, err := provider.RuntimeConfig(ctx)
			if err != nil || available {
				t.Fatalf("unexpected Cloud runtime availability: %v, %v", available, err)
			}
			personal := []SelectedRuntimeModel{{ModelType: "llm", ProviderName: "openai", ModelName: "personal", BaseURL: "https://personal.example/v1", APIKey: "personal-test-key"}}
			if got := FillMissingRoles(personal, config); !reflect.DeepEqual(got, personal) {
				t.Fatal("Cloud state changed the personal model or credentials")
			}
			catalog, err := provider.CloudModelCatalog(ctx)
			if err != nil || catalog.Known || len(catalog.Models) != 0 {
				t.Fatal("unavailable Cloud leaked into the model list")
			}
			readiness, err := provider.CloudModelReadiness(ctx, "llm")
			if err != nil || readiness.Known {
				t.Fatal("Cloud unavailability changed local readiness semantics")
			}
			if calls != 0 {
				t.Fatalf("unavailable Cloud made %d business requests", calls)
			}
			if scenario == "unreachable" || scenario == "reauth_required" {
				if token, err := store.Load(ctx); err != nil || token != "saved-refresh" {
					t.Fatal("local model lookup erased the stored Cloud session")
				}
			}
		})
	}
}
