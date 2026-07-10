package memory

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/resourcefs"
)

func memoryResourceRef(userID string) resourcefs.ResourceRef {
	return resourcefs.ResourceRef{UserID: strings.TrimSpace(userID), ResourceType: resourcefs.ResourceTypeMemory}
}

func ensureMemoryResource(ctx context.Context, db *gorm.DB, userID, userName string) (*orm.SystemMemory, *resourcefs.Service, resourcefs.ResourceState, error) {
	row, err := evolution.EnsureSystemMemory(ctx, db, userID, userName)
	if err != nil {
		return nil, nil, resourcefs.ResourceState{}, err
	}
	service := resourcefs.NewService(resourcefs.ServiceDeps{DB: db})
	state, err := service.EnsureResource(ctx, memoryResourceRef(userID), row.Content)
	if err != nil {
		return nil, nil, resourcefs.ResourceState{}, err
	}
	if strings.TrimSpace(row.DraftStatus) == "pending_confirm" {
		if draft, readErr := service.ReadFile(ctx, resourcefs.ReadFileRequest{Ref: memoryResourceRef(userID), RefType: resourcefs.FileRefDraft}); readErr == nil && strings.TrimSpace(draft.DraftStatus) == "" {
			_, _ = service.WriteDraft(ctx, resourcefs.WriteDraftRequest{
				Ref:                  memoryResourceRef(userID),
				Content:              row.DraftContent,
				ExpectedDraftVersion: draft.DraftVersion,
				UpdatedBy:            userID,
			})
		}
	}
	return row, service, state, nil
}

func memoryDraftIsPending(ctx context.Context, service *resourcefs.Service, userID string) (resourcefs.FileResponse, bool, error) {
	draft, err := service.ReadFile(ctx, resourcefs.ReadFileRequest{Ref: memoryResourceRef(userID), RefType: resourcefs.FileRefDraft})
	if err != nil {
		return resourcefs.FileResponse{}, false, err
	}
	return draft, strings.TrimSpace(draft.DraftStatus) == "pending_confirm", nil
}

func memoryCurrentWritableContent(ctx context.Context, service *resourcefs.Service, userID string) (resourcefs.FileResponse, error) {
	draft, pending, err := memoryDraftIsPending(ctx, service, userID)
	if err != nil {
		return resourcefs.FileResponse{}, err
	}
	if pending {
		return draft, nil
	}
	return service.ReadFile(ctx, resourcefs.ReadFileRequest{Ref: memoryResourceRef(userID), RefType: resourcefs.FileRefHead})
}
