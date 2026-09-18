package cloudresource

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"lazymind/core/cloudbinding"
	"lazymind/core/cloudclient"
	"lazymind/core/cloudpackage"
)

// Check the public list payload, so the existence bit cannot be inferred from
// only a matching hash or the rows visible on the frontend's current page.
func TestDesktopLocalPresenceSurvivesContentChanges(t *testing.T) {
	for _, kind := range []string{"skill", "workflow"} {
		for _, state := range []struct {
			name, cloudHash, localHash, schema string
			exists                             bool
		}{
			{"current", "a", "a", cloudpackage.FormatSchemaV2, true},
			{"local_modified", "a", "b", cloudpackage.FormatSchemaV2, true},
			{"cloud_updated", "b", "a", cloudpackage.FormatSchemaV2, true},
			{"diverged", "b", "c", cloudpackage.FormatSchemaV2, true},
			{"incompatible_but_local_exists", "a", "a", "future-schema", true},
			{"removed_local", "a", "", cloudpackage.FormatSchemaV2, false},
		} {
			t.Run(kind+"/"+state.name, func(t *testing.T) {
				cloud := &fakeCloud{page: cloudclient.ResourcePage{Items: []cloudclient.PrivateResource{{
					ResourceID: "cloud-a", ResourceType: kind, ResourceName: "name-before-local-rename",
					ContentHash: strings.Repeat(state.cloudHash, 64), FormatSchema: state.schema,
				}}, NextCursor: "next-page"}}
				bindings := &fakeBindings{rows: map[string]cloudbinding.Binding{"cloud-a": {
					CloudResourceID: "cloud-a", LocalResourceID: "local-outside-current-page",
					CloudContentHash: strings.Repeat("a", 64), InstalledLocalContentHash: strings.Repeat("a", 64),
				}}}
				adapter := &fakeAdapter{probes: map[string]LocalSnapshot{"local-outside-current-page": {
					Exists: state.exists, ResourceID: "local-outside-current-page", ResourceRef: "user:owner:renamed",
					ContentHash: strings.Repeat(state.localHash, 64),
				}}}
				page, err := (Service{Session: fakeSession{token: "fixture"}, Cloud: cloud, Bindings: bindings}).List(context.Background(), ListRequest{
					OwnerUserID: "owner", ResourceType: kind, PageSize: 100, Adapter: adapter,
				})
				if err != nil {
					t.Fatal(err)
				}
				body, err := json.Marshal(page)
				if err != nil {
					t.Fatal(err)
				}
				var wire struct {
					Items []struct {
						LocalExists *bool  `json:"local_exists"`
						LocalID     string `json:"local_resource_id"`
					} `json:"items"`
					NextCursor string `json:"next_cursor"`
				}
				if err := json.Unmarshal(body, &wire); err != nil {
					t.Fatal(err)
				}
				if len(wire.Items) != 1 || wire.Items[0].LocalExists == nil {
					t.Fatalf("list must explicitly expose local_exists: %s", body)
				}
				if *wire.Items[0].LocalExists != state.exists {
					t.Fatalf("local_exists=%v, want %v", *wire.Items[0].LocalExists, state.exists)
				}
				if state.exists && wire.Items[0].LocalID != "local-outside-current-page" {
					t.Fatalf("lost local identity: %s", body)
				}
				if wire.NextCursor != "next-page" {
					t.Fatal("presence calculation lost pagination")
				}
				if adapter.importCalls != 0 || cloud.authorizeCalls != 0 || cloud.downloadCalls != 0 {
					t.Fatal("list triggered an import or transfer")
				}
			})
		}
	}
}

type failingPresenceAdapter struct{ fakeAdapter }

func (*failingPresenceAdapter) Probe(context.Context, string, string) (LocalSnapshot, error) {
	return LocalSnapshot{}, errors.New("fixture storage unavailable")
}

func TestDesktopPresenceFailureIsNotReportedAsMissing(t *testing.T) {
	service := Service{Session: fakeSession{token: "fixture"}, Cloud: &fakeCloud{page: cloudclient.ResourcePage{Items: []cloudclient.PrivateResource{{ResourceID: "cloud-a", ResourceType: "skill"}}}}, Bindings: &fakeBindings{rows: map[string]cloudbinding.Binding{"cloud-a": {CloudResourceID: "cloud-a", LocalResourceID: "local-a"}}}}
	if _, err := service.List(context.Background(), ListRequest{OwnerUserID: "owner", ResourceType: "skill", Adapter: &failingPresenceAdapter{}}); err == nil {
		t.Fatal("failed local query was treated as a missing local skill")
	}
}

func TestDesktopUnboundSameNameIsStillCloudOnly(t *testing.T) {
	service := Service{Session: fakeSession{token: "fixture"}, Cloud: &fakeCloud{page: cloudclient.ResourcePage{Items: []cloudclient.PrivateResource{{ResourceID: "cloud-a", ResourceType: "skill", ResourceName: "same-name", FormatSchema: cloudpackage.FormatSchemaV2}}}}, Bindings: &fakeBindings{}}
	page, err := service.List(context.Background(), ListRequest{OwnerUserID: "owner", ResourceType: "skill", Adapter: &fakeAdapter{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].LocalResourceID != "" || page.Items[0].PresenceStatus != cloudbinding.PresenceDownloadRequired {
		t.Fatalf("unbound resource was hidden: %+v", page)
	}
}
