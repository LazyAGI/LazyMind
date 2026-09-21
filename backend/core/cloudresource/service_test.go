package cloudresource

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"lazymind/core/cloudbinding"
	"lazymind/core/cloudclient"
	"lazymind/core/cloudpackage"
)

type fakeSession struct {
	token string
	err   error
}

func (s fakeSession) AccessToken(context.Context, time.Duration) (string, error) {
	return s.token, s.err
}

type fakeBindings struct {
	rows map[string]cloudbinding.Binding
}

func (s *fakeBindings) FindByCloudIDs(_ context.Context, _, _, _ string, ids []string) (map[string]cloudbinding.Binding, error) {
	out := map[string]cloudbinding.Binding{}
	for _, id := range ids {
		if row, ok := s.rows[id]; ok {
			out[id] = row
		}
	}
	return out, nil
}

func (s *fakeBindings) FindByLocalID(context.Context, string, string, string, string) (cloudbinding.Binding, bool, error) {
	return cloudbinding.Binding{}, false, nil
}

func (s *fakeBindings) Upsert(_ context.Context, binding cloudbinding.Binding) (cloudbinding.Binding, error) {
	if s.rows == nil {
		s.rows = map[string]cloudbinding.Binding{}
	}
	s.rows[binding.CloudResourceID] = binding
	return binding, nil
}

type fakeAdapter struct {
	probes      map[string]LocalSnapshot
	candidate   *LocalSnapshot
	imported    LocalSnapshot
	importCalls int
}

func (a *fakeAdapter) Probe(_ context.Context, _, localID string) (LocalSnapshot, error) {
	return a.probes[localID], nil
}

func (a *fakeAdapter) FindExact(context.Context, string, string, string) (*LocalSnapshot, error) {
	return a.candidate, nil
}

func (a *fakeAdapter) ImportAndBind(context.Context, ImportRequest) (LocalSnapshot, error) {
	a.importCalls++
	return a.imported, nil
}

type fakeCloud struct {
	resource       cloudclient.PrivateResource
	page           cloudclient.ResourcePage
	downloadPath   string
	authorizations []cloudclient.DownloadAuthorization
	authorizeCalls int
	downloadCalls  int
	accountCalls   int
}

func (c *fakeCloud) Origin() string { return "https://cloud.example" }
func (c *fakeCloud) GetCurrentAccount(context.Context, string) (cloudclient.Account, error) {
	c.accountCalls++
	return cloudclient.Account{ID: "account-a"}, nil
}
func (c *fakeCloud) ListResources(context.Context, string, cloudclient.ResourceQuery) (cloudclient.ResourcePage, error) {
	return c.page, nil
}
func (c *fakeCloud) GetResource(context.Context, string, string) (cloudclient.PrivateResource, string, error) {
	return c.resource, `"etag-a"`, nil
}
func (c *fakeCloud) AuthorizeResourceDownload(context.Context, string, string, string) (cloudclient.DownloadAuthorization, error) {
	c.authorizeCalls++
	if len(c.authorizations) >= c.authorizeCalls {
		return c.authorizations[c.authorizeCalls-1], nil
	}
	return cloudclient.DownloadAuthorization{Status: cloudclient.DownloadReady, Request: &cloudclient.SignedRequest{}}, nil
}
func (c *fakeCloud) DownloadSignedRequest(context.Context, cloudclient.SignedRequest, int64, string, int64) (cloudclient.DownloadedObject, error) {
	c.downloadCalls++
	return cloudclient.DownloadedObject{Path: c.downloadPath}, nil
}

func TestDownloadPresentCurrentSkipsAuthorizationAndObjectGET(t *testing.T) {
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cloud := &fakeCloud{resource: cloudclient.PrivateResource{
		ResourceID: "resource-a", ResourceType: "skill", ResourceName: "A", ContentHash: hash,
		ContentSize: 10, FormatSchema: cloudpackage.FormatSchemaV2, ClientResourceKey: "skill:a",
	}}
	bindings := &fakeBindings{rows: map[string]cloudbinding.Binding{"resource-a": {
		CloudResourceID: "resource-a", CloudContentHash: hash, LocalResourceID: "local-a", InstalledLocalContentHash: hash,
	}}}
	adapter := &fakeAdapter{probes: map[string]LocalSnapshot{"local-a": {
		Exists: true, ResourceID: "local-a", RevisionID: "revision-a", ContentHash: hash,
	}}}
	result, err := (Service{Session: fakeSession{token: "token"}, Cloud: cloud, Bindings: bindings}).Download(context.Background(), DownloadRequest{
		OwnerUserID: "local-user", ResourceType: "skill", ResourceID: "resource-a", Adapter: adapter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AlreadyPresent || cloud.authorizeCalls != 0 || cloud.downloadCalls != 0 || adapter.importCalls != 0 {
		t.Fatalf("result=%+v auth=%d download=%d import=%d", result, cloud.authorizeCalls, cloud.downloadCalls, adapter.importCalls)
	}
}

func TestDownloadVerifiesZIPBeforeAtomicAdapterImport(t *testing.T) {
	prepared, err := cloudpackage.Prepare(cloudpackage.PrepareInput{
		ResourceType: "skill", ResourceName: "A", ClientResourceKey: "skill:a", DesktopVersion: "1.0.0",
		Files: map[string]cloudpackage.File{"SKILL.md": {Data: []byte("fixture")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	zipped, err := cloudpackage.WriteZIP(prepared)
	if err != nil {
		t.Fatal(err)
	}
	cloud := &fakeCloud{downloadPath: zipped.ZIPPath, resource: cloudclient.PrivateResource{
		ResourceID: "resource-a", ResourceType: "skill", ResourceName: "A",
		ContentHash: prepared.Manifest.ContentHash, ContentSize: prepared.Manifest.ContentSize,
		FormatSchema: cloudpackage.FormatSchemaV2, ClientResourceKey: "skill:a",
	}}
	adapter := &fakeAdapter{probes: map[string]LocalSnapshot{}, imported: LocalSnapshot{Exists: true, ResourceID: "local-new", RevisionID: "revision-new", ContentHash: prepared.Manifest.ContentHash}}
	result, err := (Service{Session: fakeSession{token: "token"}, Cloud: cloud, Bindings: &fakeBindings{rows: map[string]cloudbinding.Binding{}}, DesktopVersion: "1.0.0"}).Download(context.Background(), DownloadRequest{
		OwnerUserID: "local-user", ResourceType: "skill", ResourceID: "resource-a", Adapter: adapter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.LocalResourceID != "local-new" || adapter.importCalls != 1 || cloud.authorizeCalls != 1 || cloud.downloadCalls != 1 {
		t.Fatalf("result=%+v auth=%d download=%d import=%d", result, cloud.authorizeCalls, cloud.downloadCalls, adapter.importCalls)
	}
	if _, err := os.Stat(zipped.ZIPPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary ZIP was not removed: %v", err)
	}
}

func TestDirectDownloadAdoptsExactLocalMatchWithoutTransfer(t *testing.T) {
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cloud := &fakeCloud{resource: cloudclient.PrivateResource{
		ResourceID: "resource-a", ResourceType: "workflow", ResourceName: "A", ContentHash: hash,
		ContentSize: 10, FormatSchema: cloudpackage.FormatSchemaV2, ClientResourceKey: "workflow:a",
	}}
	bindings := &fakeBindings{rows: map[string]cloudbinding.Binding{}}
	adapter := &fakeAdapter{candidate: &LocalSnapshot{
		Exists: true, ResourceID: "local-a", ResourceRef: "user:local:a", RevisionID: "revision-a", ContentHash: hash,
	}}
	result, err := (Service{Session: fakeSession{token: "token"}, Cloud: cloud, Bindings: bindings}).Download(context.Background(), DownloadRequest{
		OwnerUserID: "local-user", ResourceType: "workflow", ResourceID: "resource-a", Adapter: adapter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AlreadyPresent || cloud.authorizeCalls != 0 || cloud.downloadCalls != 0 || adapter.importCalls != 0 {
		t.Fatalf("result=%+v auth=%d download=%d import=%d", result, cloud.authorizeCalls, cloud.downloadCalls, adapter.importCalls)
	}
	if bindings.rows["resource-a"].LocalResourceID != "local-a" {
		t.Fatalf("binding = %+v", bindings.rows["resource-a"])
	}
}

func TestSignedOutListMakesNoCloudRequest(t *testing.T) {
	cloud := &fakeCloud{}
	_, err := (Service{Session: fakeSession{err: errors.New("signed out")}, Cloud: cloud, Bindings: &fakeBindings{}}).List(context.Background(), ListRequest{
		OwnerUserID: "local-user", ResourceType: "workflow", Adapter: &fakeAdapter{},
	})
	if !errors.Is(err, ErrSessionRequired) {
		t.Fatalf("err = %v", err)
	}
	if cloud.accountCalls != 0 {
		t.Fatalf("Cloud account calls = %d", cloud.accountCalls)
	}
}

func TestDownloadPollsPreparingAuthorizationWithBoundedWait(t *testing.T) {
	cloud := &fakeCloud{authorizations: []cloudclient.DownloadAuthorization{
		{Status: cloudclient.DownloadPreparing, RetryAfterSeconds: 2},
		{Status: cloudclient.DownloadReady, Request: &cloudclient.SignedRequest{}},
	}}
	waits := 0
	service := Service{Cloud: cloud, MaxPolls: 3, Wait: func(_ context.Context, duration time.Duration) error {
		waits++
		if duration != 2*time.Second {
			t.Fatalf("wait duration = %s", duration)
		}
		return nil
	}}
	authorization, err := service.waitForDownloadAuthorization(context.Background(), "token", "resource", `"etag"`)
	if err != nil {
		t.Fatal(err)
	}
	if authorization.Status != cloudclient.DownloadReady || cloud.authorizeCalls != 2 || waits != 1 {
		t.Fatalf("authorization=%+v calls=%d waits=%d", authorization, cloud.authorizeCalls, waits)
	}
}
