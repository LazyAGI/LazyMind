package doc

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"io"
	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/paperguide"
	"lazymind/core/pdfir"
	"lazymind/core/readingpolicy"
	"lazymind/core/store"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func PaperGuideEnabled() bool {
	enabled, _ := strconv.ParseBool(os.Getenv("LAZYMIND_PAPER_GUIDE_ENABLED"))
	return enabled
}
func paperGuideCaller(ctx context.Context, user string) (DatasetCatalogCaller, error) {
	identity, e := common.GetCurrentIdentity(ctx, user)
	if e != nil {
		return DatasetCatalogCaller{}, paperguide.ErrForbidden
	}
	return DatasetCatalogCaller{UserID: user, TenantID: identity.TenantID, UserRole: identity.Role}, nil
}
func authorizePaperGuide(ctx context.Context, scope paperguide.Scope, generate bool) error {
	caller, e := paperGuideCaller(ctx, scope.OwnerID)
	if e != nil {
		return e
	}
	if _, e = ResolveResourceDataset(ctx, store.DB(), caller, scope.DatasetID, acl.PermissionDatasetRead); e != nil {
		return paperguide.ErrForbidden
	}
	var row orm.Document
	if e = store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", scope.DocumentID, scope.DatasetID).Take(&row).Error; e != nil {
		return paperguide.ErrForbidden
	}
	if generate {
		if !PaperGuideEnabled() {
			return paperguide.ErrDisabled
		}
		if readingpolicy.Check(ctx, store.DB(), scope.OwnerID, readingpolicy.PaperGuide) != nil {
			return paperguide.ErrDisabled
		}
	}
	return nil
}

type paperGuideSource struct{}

func (paperGuideSource) Load(ctx context.Context, scope paperguide.Scope, prepare bool) (paperguide.SourceState, error) {
	out := paperguide.SourceState{}
	if e := authorizePaperGuide(ctx, scope, false); e != nil {
		return out, e
	}
	row, ext, e := loadDocumentFileMeta(ctx, scope.DatasetID, scope.DocumentID)
	if e != nil {
		return out, paperguide.ErrForbidden
	}
	hash, e := pdfIRHash(ext)
	if e != nil {
		return out, paperguide.ErrSourceChanged
	}
	out.Hash = hash
	status, e := pdfIRStatus(ctx, row, hash)
	if e != nil {
		return out, e
	}
	out.DependencyFailed = status.Status == "failed"
	if status.Document != nil {
		snapshot, e := paperguide.BuildSourceSnapshot(*status.Document, paperguide.SourceIdentity{SourceSHA256: hash, OwnerID: scope.OwnerID, DatasetID: scope.DatasetID, DocumentID: scope.DocumentID})
		if e != nil {
			return out, paperguide.ErrEvidence
		}
		dir := filepath.Join(filepath.Dir(pdfIRSource(ext)), ".lazymind-artifacts", row.ID, "paper-guides", pdfir.Digest(scope.OwnerID))
		path := filepath.Join(dir, hash+"-"+snapshot.IRDigest+".json")
		// This immutable personal snapshot is independent of the shared parser cache.
		// It contains no original PDF copy or model credentials.
		if _, e = os.Stat(path); prepare && errors.Is(e, os.ErrNotExist) {
			if e = os.MkdirAll(dir, 0700); e != nil {
				return out, e
			}
			raw, e := json.Marshal(snapshot)
			if e != nil {
				return out, e
			}
			f, e := os.CreateTemp(dir, "snapshot-*.tmp")
			if e != nil {
				return out, e
			}
			temp := f.Name()
			defer os.Remove(temp)
			if _, e = f.Write(raw); e == nil {
				e = f.Sync()
			}
			closeErr := f.Close()
			if e != nil {
				return out, e
			}
			if closeErr != nil {
				return out, closeErr
			}
			if e = os.Rename(temp, path); e != nil {
				return out, e
			}
		}
		out.Snapshot = &snapshot
		out.Path = path
		return out, nil
	}
	if !prepare {
		caller, err := paperGuideCaller(ctx, scope.OwnerID)
		if err == nil {
			_, err = ResolveResourceDataset(ctx, store.DB(), caller, scope.DatasetID, acl.PermissionDatasetWrite)
			out.CanPrepare = err == nil
		}
		return out, nil
	}
	caller, e := paperGuideCaller(ctx, scope.OwnerID)
	if e != nil {
		return out, e
	}
	if _, e = ResolveResourceDataset(ctx, store.DB(), caller, scope.DatasetID, acl.PermissionDatasetWrite); e != nil {
		return out, paperguide.ErrPreparation
	}
	if e = readingpolicy.Check(ctx, store.DB(), scope.OwnerID, readingpolicy.PDFParse); e != nil {
		return out, paperguide.ErrPreparation
	}
	cfg, e := loadReadingParser(ctx, scope.OwnerID)
	if e != nil {
		return out, paperguide.ErrPreparation
	}
	if _, e = pdfir.FromConfig(cfg); e != nil {
		return out, paperguide.ErrPreparation
	}
	resource := pdfIRResource(row.DatasetID, row.ID)
	job, e := asyncjob.Enqueue(ctx, store.DB(), asyncjob.EnqueueRequest{JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: resource, IdempotencyKey: pdfir.Digest(resource + hash + pdfir.Revision), Payload: pdfIRPayload{DatasetID: row.DatasetID, DocumentID: row.ID, SourceSHA256: hash, Revision: pdfir.Revision}, MaxAttempts: 2, SkipSucceeded: true, CreateUserID: scope.OwnerID})
	if e != nil {
		return out, e
	}
	out.DependencyID = job.ID
	return out, nil
}
func (paperGuideSource) Read(ctx context.Context, path string) (paperguide.GuideSourceSnapshot, error) {
	var snap paperguide.GuideSourceSnapshot
	if path == "" {
		return snap, paperguide.ErrPreparation
	}
	f, e := os.Open(path)
	if e != nil {
		return snap, paperguide.ErrEvidence
	}
	defer f.Close()
	e = json.NewDecoder(io.LimitReader(f, 64<<20)).Decode(&snap)
	return snap, e
}
func resolvePaperGuideModel(ctx context.Context, scope paperguide.Scope, ref string) (paperguide.ModelIdentity, map[string]any, error) {
	if ref == "" {
		policy, e := readingpolicy.Load(ctx, store.DB(), scope.OwnerID)
		if e != nil {
			return paperguide.ModelIdentity{}, nil, e
		}
		ref = policy.StudyModelRef
	}
	selected, e := modelconfig.ResolveReadingStudyModel(ctx, store.DB(), scope.OwnerID, ref)
	if e != nil || selected == nil {
		return paperguide.ModelIdentity{}, nil, paperguide.ErrModelChanged
	}
	candidate := modelconfig.PrepareSelectedChatCandidate(ctx, store.DB(), *selected)
	if candidate == nil {
		return paperguide.ModelIdentity{}, nil, paperguide.ErrModelChanged
	}
	tokens := 32768
	for _, value := range []*string{selected.MaxInputTokens, selected.InputTokenLimit} {
		if value != nil {
			if n, e := strconv.Atoi(strings.TrimSpace(*value)); e == nil && n > 0 && (tokens == 32768 || n < tokens) {
				tokens = n
			}
		}
	}
	// Only a one-way hash of non-secret settings/endpoint enters persistence.
	// Credential rotation alone does not change the frozen model identity.
	parameters, _ := json.Marshal([]any{safeGuideModelConfig(candidate.LLMConfig), selected.ProviderID, selected.ProviderGroupID})
	protocol := ""
	if llm, ok := candidate.LLMConfig["llm"].(map[string]any); ok {
		protocol, _ = llm["source"].(string)
	}
	identity := paperguide.ModelIdentity{Ref: modelconfig.ChatCandidateID(selected.Source, selected.ID), ID: selected.ID, Name: selected.ModelName, Protocol: protocol, ParametersHash: pdfir.Digest(string(parameters)), ContextTokens: tokens}
	return identity, candidate.LLMConfig, nil
}

// Network identity and filesystem checks run immediately before entering the
// checkpoint. This second SQL check observes a deletion or ACL revocation
// committed since that check, without holding a transaction across network I/O.
func paperGuideCheckpointGuard(ctx context.Context, tx *gorm.DB, guide orm.PaperGuide) error {
	var row orm.Document
	if err := tx.WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", guide.DocumentID, guide.DatasetID).Take(&row).Error; err != nil {
		return paperguide.ErrForbidden
	}
	var dataset orm.Dataset
	if err := tx.Where("id = ? AND deleted_at IS NULL", guide.DatasetID).Take(&dataset).Error; err != nil {
		return paperguide.ErrForbidden
	}
	if dataset.CreateUserID != guide.OwnerID {
		permissions, _ := acl.PermissionsForWithDB(tx, acl.ResourceTypeDB, guide.DatasetID, guide.OwnerID, nil)
		allowed := false
		for _, permission := range permissions {
			if permission == acl.PermissionDatasetRead || permission == acl.PermissionDatasetWrite || permission == acl.PermissionDatasetUpload {
				allowed = true
			}
		}
		if !allowed {
			return paperguide.ErrForbidden
		}
	}
	return nil
}

func safeGuideModelConfig(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			lower := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			if strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") || strings.Contains(lower, "secret") || strings.Contains(lower, "credential") || strings.Contains(lower, "password") || strings.Contains(lower, "authorization") || strings.Contains(lower, "cookie") || lower == "access_token" || lower == "refresh_token" || lower == "token" {
				continue
			}
			if strings.Contains(lower, "url") {
				if raw, ok := item.(string); ok {
					if parsed, err := url.Parse(raw); err == nil {
						parsed.User = nil
						query := parsed.Query()
						for name := range query {
							n := strings.ToLower(name)
							if strings.Contains(n, "key") || strings.Contains(n, "token") || strings.Contains(n, "secret") || strings.Contains(n, "auth") {
								query.Del(name)
							}
						}
						parsed.RawQuery = query.Encode()
						item = parsed.String()
					}
				}
			}
			out[key] = safeGuideModelConfig(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = safeGuideModelConfig(item)
		}
		return out
	default:
		return value
	}
}
