package doc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

func TestChatSourceAttachmentCallerTenantMustMatchLibrary(t *testing.T) {
	for _, tc := range []struct {
		name, user, datasetTenant, callerTenant string
		allowed                                 bool
	}{
		{"owner in another tenant", "owner", "tenant-a", "tenant-b", false},
		{"shared uploader in another tenant", "contributor", "tenant-a", "tenant-b", false},
		{"owner in matching tenant", "owner", "tenant-a", "tenant-a", true},
		{"shared uploader in matching tenant", "contributor", "tenant-a", "tenant-a", true},
		{"blank caller uses default tenant", "owner", "default", "", true},
		{"trimmed caller uses default tenant", "owner", "default", " default ", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, scanPermissionCalls := aclIndependenceFixture(t, false)
			aclIndependenceGrant(t, db, "source", "contributor", acl.PermissionDatasetUpload)
			if err := db.Model(&orm.Dataset{}).Where("id = ?", "source").Update("tenant_id", tc.datasetTenant).Error; err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(buildDatasetDocFileDir(tc.datasetTenant, "source", "", "attachment"), "source.txt")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("tenant-scoped attachment"), 0600); err != nil {
				t.Fatal(err)
			}
			caller := aclIndependenceCaller(tc.user)
			caller.TenantID = tc.callerTenant
			references := []ChatSourceAttachmentReference{{Path: path, DatasetID: "source", Scope: ChatSourceAttachmentScopeDataset}}
			resolved, err := ValidateChatSourceAttachments(t.Context(), db.DB, caller, references)
			if tc.allowed {
				if err != nil || len(resolved) != 1 {
					t.Errorf("same-tenant attachment was rejected: refs=%v error=%v", resolved, err)
				}
			} else if err == nil || (!errors.Is(err, ErrChatSourceAttachmentForbidden) && !errors.Is(err, ErrChatSourceAttachmentUnavailable)) {
				t.Errorf("cross-tenant attachment was not denied: refs=%v error=%v", resolved, err)
			}
			if *scanPermissionCalls != 0 {
				t.Errorf("Core tenant authorization delegated to Scan %d times", *scanPermissionCalls)
			}
		})
	}
}
