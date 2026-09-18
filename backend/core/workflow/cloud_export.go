package workflow

import (
	"context"
	"errors"
	"strings"

	"lazymind/core/cloudpackage"
	workflowstore "lazymind/core/workflow/store"
)

type CloudWorkflowPackageReader interface {
	GetWorkflowPackage(context.Context, string, string, string) (workflowstore.WorkflowPackage, error)
}

type CloudWorkflowExportRequest struct {
	OwnerUserID    string
	WorkflowRef    string
	DesktopVersion string
	Reader         CloudWorkflowPackageReader
}

func PrepareCloudWorkflowPackage(ctx context.Context, request CloudWorkflowExportRequest) (cloudpackage.Prepared, error) {
	if request.Reader == nil || strings.TrimSpace(request.OwnerUserID) == "" || strings.TrimSpace(request.WorkflowRef) == "" {
		return cloudpackage.Prepared{}, errors.New("cloud Workflow export request is incomplete")
	}
	value, err := request.Reader.GetWorkflowPackage(ctx, strings.TrimSpace(request.OwnerUserID), strings.TrimSpace(request.WorkflowRef), "")
	if err != nil {
		return cloudpackage.Prepared{}, err
	}
	files := make(map[string]cloudpackage.File, len(value.Files))
	for path, body := range value.Files {
		if isWorkflowRuntimeFile(path) {
			continue
		}
		files[path] = cloudpackage.File{Data: body}
	}
	name := strings.TrimSpace(value.Name)
	if name == "" {
		name = strings.TrimSpace(value.WorkflowID)
	}
	clientResourceID := strings.TrimSpace(value.ResourceID)
	if clientResourceID == "" {
		clientResourceID = strings.TrimSpace(value.WorkflowRef)
	}
	return cloudpackage.Prepare(cloudpackage.PrepareInput{
		ResourceType: "workflow", ResourceName: name,
		ClientResourceKey: "workflow:" + clientResourceID,
		DesktopVersion:    strings.TrimSpace(request.DesktopVersion), Files: files,
	})
}

func isWorkflowRuntimeFile(path string) bool {
	switch strings.TrimSpace(path) {
	case "compiled_graph.json", "session.json", "artifacts.json":
		return true
	default:
		return strings.HasPrefix(path, "runtime/") || strings.HasPrefix(path, "artifacts/")
	}
}
