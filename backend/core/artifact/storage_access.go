package artifact

import (
	"context"
	"path/filepath"
	"strings"

	"lazymind/core/common/orm"
	"lazymind/core/staticstorage"
	"lazymind/core/store"
)

func init() {
	staticstorage.Register(staticstorage.Namespace{Prefix: "artifacts/", Root: blobRoot, Authorize: authorizeBlob})
	// Existing signed/unsigned references remain valid during storage rollout.
	staticstorage.Register(staticstorage.Namespace{Prefix: "subagent/artifact-blobs/", Root: legacyBlobRoot, Authorize: authorizeBlob})
}

func authorizeBlob(ctx context.Context, rel, userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" || !strings.HasPrefix(rel, safeTenant(userID)+"/") {
		return false
	}
	db := store.DB()
	if db == nil || !db.Migrator().HasTable(&orm.ArtifactBlob{}) || !db.Migrator().HasTable(&orm.ArtifactV2{}) {
		return true
	}
	digest := filepath.Base(rel)
	var live int64
	if err := db.WithContext(ctx).Table("artifact_blobs").
		Joins("JOIN artifact_revisions ON artifact_revisions.blob_id = artifact_blobs.id").
		Joins("JOIN artifacts ON artifacts.id = artifact_revisions.artifact_id").
		Where("artifact_blobs.tenant_id = ? AND artifact_blobs.sha256 = ?", userID, digest).
		Where("artifacts.owner_user_id = ? AND artifacts.deleted_at IS NULL", userID).
		Count(&live).Error; err != nil {
		return false
	}
	if live > 0 {
		return true
	}
	var known int64
	if err := db.WithContext(ctx).Model(&orm.ArtifactBlob{}).Where("tenant_id = ? AND sha256 = ?", userID, digest).Count(&known).Error; err != nil {
		return false
	}
	return known == 0
}
