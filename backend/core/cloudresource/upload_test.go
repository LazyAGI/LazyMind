package cloudresource

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"lazymind/core/cloudbinding"
	"lazymind/core/cloudclient"
	"lazymind/core/cloudpackage"
)

type fakeUploadAdapter struct {
	prepared     UploadPackage
	prepareCalls int
}

func (a *fakeUploadAdapter) PrepareUpload(context.Context, string, string) (UploadPackage, error) {
	a.prepareCalls++
	return a.prepared, nil
}

type fakeUploadBindings struct {
	local  *cloudbinding.Binding
	stored []cloudbinding.Binding
}

func (s *fakeUploadBindings) FindByCloudIDs(context.Context, string, string, string, []string) (map[string]cloudbinding.Binding, error) {
	return map[string]cloudbinding.Binding{}, nil
}

func (s *fakeUploadBindings) FindByLocalID(context.Context, string, string, string, string) (cloudbinding.Binding, bool, error) {
	if s.local == nil {
		return cloudbinding.Binding{}, false, nil
	}
	return *s.local, true, nil
}

func (s *fakeUploadBindings) Upsert(_ context.Context, binding cloudbinding.Binding) (cloudbinding.Binding, error) {
	s.stored = append(s.stored, binding)
	return binding, nil
}

type fakeUploadCloud struct {
	page          cloudclient.ResourcePage
	resource      cloudclient.PrivateResource
	beginResult   cloudclient.ResourceOperation
	finalResult   cloudclient.ResourceOperation
	beginCalls    int
	putCalls      int
	completeCalls int
	uploadPath    string
}

func (c *fakeUploadCloud) Origin() string { return "https://cloud.example" }
func (c *fakeUploadCloud) GetCurrentAccount(context.Context, string) (cloudclient.Account, error) {
	return cloudclient.Account{ID: "account-a"}, nil
}
func (c *fakeUploadCloud) ListResources(context.Context, string, cloudclient.ResourceQuery) (cloudclient.ResourcePage, error) {
	return c.page, nil
}
func (c *fakeUploadCloud) GetResource(context.Context, string, string) (cloudclient.PrivateResource, string, error) {
	return c.resource, `"` + c.resource.ContentHash + `"`, nil
}
func (c *fakeUploadCloud) BeginResourceUpsert(_ context.Context, _ string, _ cloudclient.ResourceUpsertRequest) (cloudclient.ResourceOperation, error) {
	c.beginCalls++
	return c.beginResult, nil
}
func (c *fakeUploadCloud) PresignUploadParts(context.Context, string, string, []int) ([]cloudclient.UploadPartAuthorization, error) {
	return nil, errors.New("multipart not expected in this fixture")
}
func (c *fakeUploadCloud) PutSignedFile(_ context.Context, _ cloudclient.SignedRequest, path string, _, _ int64) (string, error) {
	c.putCalls++
	c.uploadPath = path
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return `"fixture-etag"`, nil
}
func (c *fakeUploadCloud) CompleteResourceUpload(context.Context, string, string, []cloudclient.CompletedUploadPart) error {
	c.completeCalls++
	return nil
}
func (c *fakeUploadCloud) GetResourceUpsertOperation(context.Context, string, string) (cloudclient.ResourceOperation, error) {
	return c.finalResult, nil
}
func (c *fakeUploadCloud) CancelResourceUpload(context.Context, string, string) error {
	return nil
}
func (c *fakeUploadCloud) AuthorizeResourceDownload(context.Context, string, string, string) (cloudclient.DownloadAuthorization, error) {
	return cloudclient.DownloadAuthorization{}, errors.New("download not expected")
}
func (c *fakeUploadCloud) DownloadSignedRequest(context.Context, cloudclient.SignedRequest, int64, string, int64) (cloudclient.DownloadedObject, error) {
	return cloudclient.DownloadedObject{}, errors.New("download not expected")
}

func preparedSkillUpload(t *testing.T) UploadPackage {
	t.Helper()
	prepared, err := cloudpackage.Prepare(cloudpackage.PrepareInput{
		ResourceType: "skill", ResourceName: "Fixture Skill", ClientResourceKey: "skill:local-a", DesktopVersion: "1.0.0",
		Files: map[string]cloudpackage.File{"SKILL.md": {Data: []byte("fixture Skill")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return UploadPackage{
		Local:    LocalSnapshot{Exists: true, ResourceID: "local-a", RevisionID: "revision-a", ContentHash: prepared.Manifest.ContentHash},
		Prepared: prepared,
	}
}

func TestUploadFirstSkillWritesObjectWaitsForSuccessAndPersistsBinding(t *testing.T) {
	packageToUpload := preparedSkillUpload(t)
	resource := cloudclient.PrivateResource{
		ResourceID: "00000000-0000-7000-8000-000000000201", ResourceType: "skill",
		ClientResourceKey: packageToUpload.Prepared.Manifest.ClientResourceKey, ResourceName: packageToUpload.Prepared.Manifest.ResourceName,
		ContentHash: packageToUpload.Prepared.Manifest.ContentHash, ContentSize: packageToUpload.Prepared.Manifest.ContentSize,
		FormatSchema: cloudpackage.FormatSchemaV2, UpdatedAt: "2026-08-24T10:00:00Z",
	}
	cloud := &fakeUploadCloud{
		beginResult: cloudclient.ResourceOperation{
			OperationID: "00000000-0000-7000-8000-000000000202", Status: cloudclient.ResourceOperationWaitingForUpload,
			Upload: &cloudclient.UploadAuthorization{
				UploadID: "00000000-0000-7000-8000-000000000203", Mode: cloudclient.UploadModeSinglePut,
				SignedRequest: &cloudclient.SignedRequest{URL: "https://objects.example/upload", Method: "PUT"},
			},
		},
		finalResult: cloudclient.ResourceOperation{OperationID: "00000000-0000-7000-8000-000000000202", Status: cloudclient.ResourceOperationSucceeded, Resource: &resource},
	}
	bindings := &fakeUploadBindings{}
	adapter := &fakeUploadAdapter{prepared: packageToUpload}
	result, err := (&Service{
		Session: fakeSession{token: "fixture-token"}, Cloud: cloud, Bindings: bindings, DesktopVersion: "1.0.0",
	}).Upload(context.Background(), UploadRequest{OwnerUserID: "local-user", ResourceType: "skill", LocalResourceID: "local-a", Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cloudbinding.UploadFirst || result.ResourceID != resource.ResourceID {
		t.Fatalf("result = %+v", result)
	}
	if cloud.beginCalls != 1 || cloud.putCalls != 1 || cloud.completeCalls != 1 || len(bindings.stored) != 1 {
		t.Fatalf("begin=%d put=%d complete=%d bindings=%d", cloud.beginCalls, cloud.putCalls, cloud.completeCalls, len(bindings.stored))
	}
	if stored := bindings.stored[0]; stored.LocalResourceID != "local-a" || stored.CloudContentHash != packageToUpload.Prepared.Manifest.ContentHash || stored.InstalledLocalRevisionID != "revision-a" {
		t.Fatalf("binding = %+v", stored)
	}
	if _, err := os.Stat(cloud.uploadPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary upload ZIP was not removed: %v", err)
	}
}

func TestUploadUnchangedSkillSkipsZIPUpsertAndObjectTransfer(t *testing.T) {
	packageToUpload := preparedSkillUpload(t)
	hash := packageToUpload.Prepared.Manifest.ContentHash
	resource := cloudclient.PrivateResource{
		ResourceID: "00000000-0000-7000-8000-000000000204", ResourceType: "skill", ClientResourceKey: "skill:local-a",
		ResourceName: "Fixture Skill", ContentHash: hash, ContentSize: packageToUpload.Prepared.Manifest.ContentSize,
		FormatSchema: cloudpackage.FormatSchemaV2, UpdatedAt: "2026-08-24T10:00:00Z",
	}
	binding := cloudbinding.Binding{
		CloudIssuer: "https://cloud.example", CloudAccountID: "account-a", ResourceType: "skill",
		CloudResourceID: resource.ResourceID, ClientResourceKey: resource.ClientResourceKey, CloudContentHash: hash,
		LocalResourceID: "local-a", InstalledLocalRevisionID: "revision-a", InstalledLocalContentHash: hash,
		CloudResourceName: resource.ResourceName,
	}
	cloud := &fakeUploadCloud{resource: resource}
	result, err := (&Service{
		Session: fakeSession{token: "fixture-token"}, Cloud: cloud, Bindings: &fakeUploadBindings{local: &binding},
	}).Upload(context.Background(), UploadRequest{
		OwnerUserID: "local-user", ResourceType: "skill", LocalResourceID: "local-a",
		Adapter: &fakeUploadAdapter{prepared: packageToUpload},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cloudbinding.UploadNotRequired || cloud.beginCalls != 0 || cloud.putCalls != 0 || cloud.completeCalls != 0 {
		t.Fatalf("result=%+v begin=%d put=%d complete=%d", result, cloud.beginCalls, cloud.putCalls, cloud.completeCalls)
	}
	if strings.TrimSpace(packageToUpload.Prepared.ZIPPath) != "" {
		t.Fatalf("duplicate upload created ZIP %q", packageToUpload.Prepared.ZIPPath)
	}
}
