package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/artifact"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

func newCapabilityOriginalService(db *gorm.DB) (*doc.OriginalService, error) {
	return doc.NewOriginalService(doc.OriginalServiceDeps{DB: db, ResolveWorkArtifact: func(ctx context.Context, caller doc.DatasetCatalogCaller, artifactID, revisionID string) (doc.OriginalArtifact, error) {
		svc := artifact.New(db)
		// Public upload supplies a fixed revision. Resolve ownership before revealing
		// whether that artifact has a published delivery. Internal callers may pin head.
		var head *orm.ArtifactHead
		var err error
		if revisionID == "" {
			head, err = svc.Head(ctx, artifactID, "published")
			if err != nil {
				return doc.OriginalArtifact{}, workOriginalLookupError(err)
			}
			revisionID = head.RevisionID
		}
		rev, art, err := svc.GetRevision(ctx, caller.UserID, revisionID)
		if err != nil {
			return doc.OriginalArtifact{}, workOriginalLookupError(err)
		}
		tenant := strings.TrimSpace(caller.TenantID)
		if tenant == "" {
			tenant = "default"
		}
		artTenant := strings.TrimSpace(art.TenantID)
		if artTenant == "" {
			artTenant = "default"
		}
		// Live Main Chat/SubAgent writers use the owner ID as a personal
		// storage namespace, not the active knowledge tenant. GetRevision has
		// already checked that owner. Explicit tenant namespaces stay strict.
		ownerNamespace := artTenant == caller.UserID && art.OwnerUserID == caller.UserID &&
			(rev.ProducerType == artifact.ProducerMainChat || rev.ProducerType == artifact.ProducerSubAgent)
		if art.ID != artifactID || (!ownerNamespace && artTenant != tenant) {
			return doc.OriginalArtifact{}, workOriginalLookupError(artifact.ErrNotFound)
		}
		if head == nil {
			head, err = svc.Head(ctx, artifactID, "published")
			if errors.Is(err, artifact.ErrNotFound) {
				return doc.OriginalArtifact{}, &doc.ResourceError{Code: string(doc.DatasetServiceInvalidArgument), Message: "work artifact revision is not published"}
			}
			if err != nil {
				return doc.OriginalArtifact{}, err
			}
		}
		published, _, err := svc.GetRevision(ctx, caller.UserID, head.RevisionID)
		if err != nil {
			return doc.OriginalArtifact{}, workOriginalLookupError(err)
		}
		if rev.RevisionNo > published.RevisionNo {
			return doc.OriginalArtifact{}, &doc.ResourceError{Code: string(doc.DatasetServiceInvalidArgument), Message: "work artifact revision is not published"}
		}
		var metadata map[string]any
		_ = json.Unmarshal(rev.Metadata, &metadata)
		name := art.Title
		if filename, ok := metadata["filename"].(string); ok && strings.TrimSpace(filename) != "" {
			name = filename
		}
		if filepath.Ext(name) == "" {
			switch rev.ContentType {
			case "text/plain":
				name += ".txt"
			case "text/markdown":
				name += ".md"
			case "text/html":
				name += ".html"
			case "application/json":
				name += ".json"
			case "application/pdf":
				name += ".pdf"
			}
		}
		result := doc.OriginalArtifact{Filename: name, ContentType: rev.ContentType, ContentSHA256: rev.ContentHash, RevisionID: rev.ID}
		if rev.BlobID != "" {
			var blob orm.ArtifactBlob
			if err := db.WithContext(ctx).Where("id = ? AND tenant_id = ? AND state = ?", rev.BlobID, art.TenantID, "ready").Take(&blob).Error; err != nil {
				return doc.OriginalArtifact{}, err
			}
			if blob.Size != rev.Size || strings.TrimPrefix(rev.ContentHash, "sha256:") != blob.SHA256 {
				return doc.OriginalArtifact{}, fmt.Errorf("artifact original metadata mismatch")
			}
			input, err := artifact.OpenBlob(artifact.BlobRef{TenantID: blob.TenantID, SHA256: blob.SHA256, Size: blob.Size, MIMEType: blob.MIMEType, StorageKey: blob.StorageKey})
			if err != nil {
				return doc.OriginalArtifact{}, err
			}
			result.Reader = input
			result.ContentType = blob.MIMEType
		} else if len(rev.InlineJSON) > 0 {
			result.Reader = io.NopCloser(bytes.NewReader(rev.InlineJSON))
			result.ContentType = "application/json"
			if filepath.Ext(result.Filename) != ".json" {
				result.Filename += ".json"
			}
		} else {
			return doc.OriginalArtifact{}, fmt.Errorf("artifact has no original bytes")
		}
		return result, nil
	}})
}

// Missing and unauthorized source identities are indistinguishable to callers;
// database failures must retain their original error for the internal-error path.
func workOriginalLookupError(err error) error {
	if errors.Is(err, artifact.ErrNotFound) || errors.Is(err, artifact.ErrAccessDenied) || errors.Is(err, gorm.ErrRecordNotFound) {
		return &doc.ResourceError{Code: string(doc.DatasetServiceNotFound), Message: "work artifact revision not found"}
	}
	return err
}
