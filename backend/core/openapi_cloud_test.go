package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/cloudclient"
	"lazymind/core/cloudresource"
	"lazymind/core/cloudsession"
	"lazymind/core/credentialvault"
)

func cloudSpecForTest(t *testing.T) map[string]any {
	t.Helper()
	router := mux.NewRouter()
	registerCoreRoutes(router)
	raw, err := buildOpenAPISpecFromRouter(router)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestOpenAPICloudProviderConnectionContracts(t *testing.T) {
	spec := cloudSpecForTest(t)
	for _, tc := range []struct{ method, path, status, schema string }{
		{"post", "/provider-connections/sessions", "201", "ProviderConnectionSession"},
		{"get", "/provider-connections/sessions/{session_id}", "200", "ProviderConnectionSession"},
		{"post", "/provider-connections/{auth_connection_id}:reauthorize", "201", "ProviderConnectionSession"},
		{"get", "/provider-connections", "200", "ProviderConnectionPage"},
		{"delete", "/provider-connections/sessions/{session_id}", "204", ""},
		{"delete", "/provider-connections/{auth_connection_id}", "204", ""},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			op := openAPIOperationForTest(t, spec, tc.method, apiPrefix+tc.path)
			responses := op["responses"].(map[string]any)
			resp, ok := responses[tc.status].(map[string]any)
			if !ok {
				t.Fatalf("missing actual status %s", tc.status)
			}
			if tc.schema == "" {
				if _, ok := resp["content"]; ok {
					t.Fatal("204 response must have no body")
				}
				return
			}
			content, ok := resp["content"].(map[string]any)
			if !ok {
				t.Fatal("success response has no content schema")
			}
			got := content["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"]
			if got != "#/components/schemas/"+tc.schema {
				t.Fatalf("response schema = %v", got)
			}
		})
	}
	create := openAPIOperationForTest(t, spec, "post", apiPrefix+"/provider-connections/sessions")
	body, ok := create["requestBody"].(map[string]any)
	if !ok || body["required"] != true {
		t.Fatal("create session must document its required JSON body")
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	request := schemas["ProviderConnectionCreateRequest"].(map[string]any)
	if !reflect.DeepEqual(request["required"], []any{"provider"}) || request["properties"].(map[string]any)["provider"].(map[string]any)["type"] != "string" {
		t.Fatal("create session must require its provider string")
	}
	if _, ok := openAPIOperationForTest(t, spec, "post", apiPrefix+"/provider-connections/{auth_connection_id}:reauthorize")["requestBody"]; ok {
		t.Fatal("reauthorization does not read a request body")
	}
}

func TestOpenAPICloudReadContracts(t *testing.T) {
	spec := cloudSpecForTest(t)
	for _, kind := range []string{"skills", "workflows"} {
		for _, tc := range []struct{ suffix, schema string }{
			{"", "CloudResourcePageResponse"}, {"/{resource_id}", "CloudResourceMetadataResponse"},
			{"/{resource_id}/tree", "CloudResourceTreeResponse"}, {"/{resource_id}/content", "CloudResourceContentResponse"},
		} {
			op := openAPIOperationForTest(t, spec, "get", apiPrefix+"/cloud/"+kind+tc.suffix)
			if got := openAPIObjectResponseRefForTest(t, op); got != "#/components/schemas/"+tc.schema {
				t.Fatalf("%s%s response = %s", kind, tc.suffix, got)
			}
			if tc.suffix != "" && !reflect.DeepEqual(op["tags"], []any{"DesktopCloud"}) {
				t.Fatal("resource read must preserve DesktopCloud client")
			}
			if strings.HasSuffix(tc.suffix, "/content") {
				for _, name := range []string{"path", "If-Match"} {
					found := false
					for _, raw := range op["parameters"].([]any) {
						p := raw.(map[string]any)
						if p["name"] == name && p["required"] == true {
							found = true
						}
					}
					if !found {
						t.Fatalf("missing required %s", name)
					}
				}
				for _, status := range []string{"412", "428"} {
					if _, ok := op["responses"].(map[string]any)[status]; !ok {
						t.Fatalf("missing %s", status)
					}
				}
			}
		}
	}
	for path, schema := range map[string]string{"/cloud/knowledge-market": "CloudKnowledgeCatalogPageResponse", "/cloud/knowledge-market/items/{catalog_key}": "CloudKnowledgeCatalogDetailResponse"} {
		op := openAPIOperationForTest(t, spec, "get", apiPrefix+path)
		if got := openAPIObjectResponseRefForTest(t, op); got != "#/components/schemas/"+schema {
			t.Fatalf("%s response = %s", path, got)
		}
		if !reflect.DeepEqual(op["tags"], []any{"DesktopCloud"}) {
			t.Fatal("knowledge market must preserve DesktopCloud client")
		}
	}
}

func TestOpenAPICloudSchemasMatchWireFields(t *testing.T) {
	spec := cloudSpecForTest(t)
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	for name, model := range map[string]any{
		"CloudSessionStatus": cloudsession.Status{}, "CloudLoginStart": cloudsession.LoginStart{},
		"CloudTokenPlanQuota": cloudclient.TokenPlanModelQuota{}, "CloudTokenPlanUsage": cloudclient.TokenPlanPeriodUsage{},
		"CredentialBackupStatus": credentialvault.BackupStatus{}, "CredentialRestoreRecord": credentialvault.RestoreRecordSummary{},
		"CredentialRestoreDiscovery": credentialvault.RestoreDiscovery{}, "CredentialRestoreOperation": credentialvault.LocalRestoreOperation{},
		"CredentialRestoreRequest": credentialvault.RestoreCommand{}, "CredentialRestoreSelection": credentialvault.RestoreSelection{},
		"ProviderConnectionSession": cloudclient.ProviderConnectionSession{}, "ProviderConnection": cloudclient.ProviderConnection{},
		"ProviderConnectionPage": cloudclient.ProviderConnectionPage{}, "CloudResourceListItem": cloudresource.ListItem{},
		"CloudResourcePage": cloudresource.ListPage{}, "CloudResourceMetadata": cloudclient.PrivateResource{},
		"CloudResourceTree": cloudclient.ResourceTree{}, "CloudResourceContent": cloudclient.ResourceFileContent{},
		"CloudResourceDownloadResult": cloudresource.DownloadResult{}, "CloudResourceUploadResult": cloudresource.UploadResult{},
		"CloudKnowledgeCatalogItem": cloudclient.KnowledgeMarketItem{}, "CloudKnowledgeCatalogDetail": cloudclient.KnowledgeMarketDetail{},
		"CloudKnowledgeCatalogPage": cloudclient.KnowledgeMarketPage{},
	} {
		t.Run(name, func(t *testing.T) {
			schema, ok := schemas[name].(map[string]any)
			if !ok {
				t.Fatalf("missing schema %s", name)
			}
			properties := schema["properties"].(map[string]any)
			want := map[string]bool{}
			var fields func(reflect.Type)
			fields = func(typ reflect.Type) {
				for i := 0; i < typ.NumField(); i++ {
					f := typ.Field(i)
					if f.Anonymous {
						fields(f.Type)
						continue
					}
					key := strings.Split(f.Tag.Get("json"), ",")[0]
					if key != "" && key != "-" {
						want[key] = true
					}
				}
			}
			fields(reflect.TypeOf(model))
			if len(want) != len(properties) {
				t.Fatalf("schema fields = %v, wire fields = %v", properties, want)
			}
			for key := range want {
				if _, ok := properties[key]; !ok {
					t.Errorf("missing wire field %s", key)
				}
			}
		})
	}
}

func TestOpenAPICloudAccountContracts(t *testing.T) {
	spec := cloudSpecForTest(t)
	for _, tc := range []struct{ method, path, schema string }{
		{"get", "/cloud/session", "CloudSessionStatus"}, {"post", "/cloud/login", "CloudLoginStart"},
		{"post", "/cloud/logout", "CloudSessionStatus"}, {"get", "/cloud/token-plan", "CloudTokenPlanSnapshot"},
		{"get", "/credential-vault/backup", "CredentialBackupStatus"}, {"post", "/credential-vault/backup:enable", "CredentialBackupStatus"},
		{"post", "/credential-vault/backup:disable", "CredentialBackupStatus"}, {"get", "/credential-vault/restores", "CredentialRestoreDiscovery"},
		{"post", "/credential-vault/restores", "CredentialRestoreOperation"}, {"get", "/credential-vault/restores/{operation_id}", "CredentialRestoreOperation"},
	} {
		op := openAPIOperationForTest(t, spec, tc.method, apiPrefix+tc.path)
		if got := openAPIObjectResponseRefForTest(t, op); got != "#/components/schemas/"+tc.schema+"Response" {
			t.Errorf("%s %s response = %s", tc.method, tc.path, got)
		}
	}
	for _, tc := range []struct{ method, path string }{
		{"delete", "/credential-vault/restores/{operation_id}"}, {"post", "/credential-vault/restores:clear-temporary"},
	} {
		op := openAPIOperationForTest(t, spec, tc.method, apiPrefix+tc.path)
		response, ok := op["responses"].(map[string]any)["204"].(map[string]any)
		if !ok || response["content"] != nil {
			t.Errorf("%s must return an empty 204", tc.path)
		}
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	status := schemas["CloudSessionStatus"].(map[string]any)["properties"].(map[string]any)
	for _, secret := range []string{"access_token", "refresh_token"} {
		if _, exists := status[secret]; exists {
			t.Errorf("session schema exposes %s", secret)
		}
	}
	op := openAPIOperationForTest(t, spec, "post", apiPrefix+"/credential-vault/restores")
	body := op["requestBody"].(map[string]any)
	if body["required"] != true {
		t.Fatal("restore body must be required")
	}
	request := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if request["$ref"] != "#/components/schemas/CredentialRestoreRequest" {
		t.Fatal("restore body must use the typed contract")
	}
}

func TestDesktopCredentialCleanupIsNotAPublicOpenAPI(t *testing.T) {
	spec := cloudSpecForTest(t)
	if _, exists := spec["paths"].(map[string]any)[apiPrefix+"/internal/credential-vault/restores:clear-temporary"]; exists {
		t.Fatal("Desktop owner cleanup must not be exposed in the public client contract")
	}
}
