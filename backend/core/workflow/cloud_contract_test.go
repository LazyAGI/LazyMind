package workflow

import (
	"context"
	"testing"

	"lazymind/core/common/orm"
	workflowstore "lazymind/core/workflow/store"
)

type cloudWorkflowPackageReader struct {
	packageValue workflowstore.WorkflowPackage
}

func (r cloudWorkflowPackageReader) GetWorkflowPackage(context.Context, string, string, string) (workflowstore.WorkflowPackage, error) {
	return r.packageValue, nil
}

func TestPrepareCloudWorkflowPackageExcludesCompiledGraphAndRuntimeState(t *testing.T) {
	prepared, err := PrepareCloudWorkflowPackage(context.Background(), CloudWorkflowExportRequest{
		OwnerUserID:    "local-user",
		WorkflowRef:    "user:local-user:fixture",
		DesktopVersion: "1.0.0",
		Reader: cloudWorkflowPackageReader{packageValue: workflowstore.WorkflowPackage{
			ResourceID: "local-workflow-id", WorkflowRef: "user:local-user:fixture", WorkflowID: "fixture", Name: "Fixture",
			Files: validCloudWorkflowFiles(),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Manifest.ResourceType != "workflow" || prepared.Manifest.Entrypoint != "workflow.yaml" {
		t.Fatalf("manifest=%+v", prepared.Manifest)
	}
	if prepared.Manifest.ClientResourceKey != "workflow:local-workflow-id" {
		t.Fatalf("client_resource_key = %q", prepared.Manifest.ClientResourceKey)
	}
	for _, forbidden := range []string{"compiled_graph.json", "session.json", "artifacts.json"} {
		if _, ok := prepared.Files[forbidden]; ok {
			t.Fatalf("runtime-only file exported: %q", forbidden)
		}
	}
}

func TestImportCloudWorkflowRecompilesAndStartsDisabled(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}, &orm.UserWorkflowSetting{}); err != nil {
		t.Fatal(err)
	}
	result, err := ImportCloudWorkflowPackage(context.Background(), CloudWorkflowImportRequest{
		DB: db.DB, OwnerUserID: "local-user", Files: validCloudWorkflowFiles(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkflowRef == "" || result.RevisionID == "" || result.GraphHash == "" {
		t.Fatalf("incomplete import result: %+v", result)
	}
	if result.Enabled {
		t.Fatal("downloaded workflow must require explicit enablement")
	}
}

func TestImportCloudWorkflowDoesNotOverwriteExistingLocalWorkflow(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}, &orm.UserWorkflowSetting{}); err != nil {
		t.Fatal(err)
	}
	request := CloudWorkflowImportRequest{DB: db.DB, OwnerUserID: "local-user", Files: validCloudWorkflowFiles()}
	if _, err := ImportCloudWorkflowPackage(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportCloudWorkflowPackage(context.Background(), request); err == nil {
		t.Fatal("repeated download must not overwrite the local Workflow")
	}
}

func validCloudWorkflowFiles() map[string][]byte {
	return map[string][]byte{
		"workflow.yaml":        []byte("id: fixture\nname: Fixture\nslots:\n  - {id: output}\nsteps:\n  - {id: write, label: Write}\n"),
		"scenario/state.yml":   []byte("transitions:\n  __start__: [{to: write}]\n  write: [{to: __end__}]\nsteps:\n  write: {outputs: [output]}\n"),
		"scenario/scenario.md": []byte("# fixture\n"),
	}
}
