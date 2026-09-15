package modelconfig

import (
	"context"
	"reflect"
	"testing"

	"lazymind/core/cloudclient"
)

type entitlementRuntimeTestClient struct {
	bootstrap cloudclient.ModelProviderBootstrap
}

func (c entitlementRuntimeTestClient) Origin() string { return "https://cloud.example" }
func (c entitlementRuntimeTestClient) GetProviderBootstrap(context.Context, string) (cloudclient.ModelProviderBootstrap, error) {
	return c.bootstrap, nil
}

func withCloudEntitlement(bootstrap cloudclient.ModelProviderBootstrap, hasPlan, chatAvailable bool, reason string) cloudclient.ModelProviderBootstrap {
	value := reflect.ValueOf(&bootstrap).Elem()
	if field := value.FieldByName("HasTokenPlan"); field.IsValid() && field.CanSet() {
		field.SetBool(hasPlan)
	}
	if field := value.FieldByName("CloudChatAvailable"); field.IsValid() && field.CanSet() {
		field.SetBool(chatAvailable)
	}
	if field := value.FieldByName("ReasonCode"); field.IsValid() && field.CanSet() && field.Kind() == reflect.String {
		field.SetString(reason)
	}
	return bootstrap
}

func TestCloudRuntimeProviderRequiresTokenPlanAndAvailableCatalog(t *testing.T) {
	models := []cloudclient.PublicCloudModel{{
		ModelKey: "cloud-text", Capabilities: []string{"chat"}, Status: "available",
	}}
	tests := []struct {
		name          string
		hasPlan       bool
		chatAvailable bool
		wantAvailable bool
	}{
		{name: "no Plan", hasPlan: false, chatAvailable: false, wantAvailable: false},
		{name: "Plan with Chat", hasPlan: true, chatAvailable: true, wantAvailable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bootstrap := withCloudEntitlement(cloudclient.ModelProviderBootstrap{
				Available: true, Models: models,
			}, test.hasPlan, test.chatAvailable, "token_plan_required")
			provider := &CloudRuntimeProvider{
				Session: runtimeTestTokens{},
				Client:  entitlementRuntimeTestClient{bootstrap: bootstrap},
			}
			config, available, err := provider.RuntimeConfig(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if available != test.wantAvailable {
				t.Errorf("available=%v want=%v config=%+v", available, test.wantAvailable, config)
			}
		})
	}
}
