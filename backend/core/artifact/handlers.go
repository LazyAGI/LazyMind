package artifact

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func serviceFromStore() *Service {
	db := store.DB()
	if db == nil {
		return nil
	}
	return New(db)
}

func requireUser(r *http.Request) string {
	userID := store.UserID(r)
	if userID == "" {
		return "0"
	}
	return userID
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrDisabled):
		common.ReplyErr(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrAccessDenied):
		common.ReplyErr(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrRevisionConflict), errors.Is(err, ErrIdempotencyConflict):
		common.ReplyErr(w, err.Error(), http.StatusConflict)
	case errors.Is(err, ErrBlobHashMismatch):
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
	default:
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
	}
}

func GetArtifact(w http.ResponseWriter, r *http.Request) {
	if !Enabled() {
		common.ReplyErr(w, ErrNotFound.Error(), http.StatusNotFound)
		return
	}
	svc := serviceFromStore()
	if svc == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	id := common.PathVar(r, "id")
	revs, art, err := svc.ListRevisions(r.Context(), requireUser(r), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	head, _ := svc.Head(r.Context(), art.ID, ChannelPublished)
	common.ReplyOK(w, map[string]any{
		"artifact_id": art.ID, "logical_key": art.LogicalKey, "title": art.Title,
		"revision_count": len(revs), "head": head,
	})
}

func ListRevisionsHTTP(w http.ResponseWriter, r *http.Request) {
	if !Enabled() {
		common.ReplyErr(w, ErrNotFound.Error(), http.StatusNotFound)
		return
	}
	svc := serviceFromStore()
	if svc == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	revs, art, err := svc.ListRevisions(r.Context(), requireUser(r), common.PathVar(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	head, _ := svc.Head(r.Context(), art.ID, ChannelPublished)
	out := make([]map[string]any, 0, len(revs))
	for _, rev := range revs {
		item := map[string]any{
			"artifact_id": art.ID, "revision_id": rev.ID, "revision_no": rev.RevisionNo,
			"content_type": rev.ContentType, "content_hash": rev.ContentHash, "size": rev.Size,
			"caption": rev.Caption, "producer_type": rev.ProducerType, "created_at": rev.CreatedAt,
			"created_by": rev.CreatedBy,
		}
		if head != nil && head.RevisionID == rev.ID {
			item["published"] = true
			item["head_version"] = head.Version
		}
		var meta map[string]any
		if json.Unmarshal(rev.Metadata, &meta) == nil {
			item["change_summary"] = meta["change_summary"]
		}
		out = append(out, item)
	}
	common.ReplyOK(w, map[string]any{"artifact_id": art.ID, "logical_key": art.LogicalKey, "revisions": out})
}

func GetRevisionHTTP(w http.ResponseWriter, r *http.Request) {
	if !Enabled() {
		common.ReplyErr(w, ErrNotFound.Error(), http.StatusNotFound)
		return
	}
	svc := serviceFromStore()
	if svc == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	rev, art, err := svc.GetRevision(r.Context(), requireUser(r), common.PathVar(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	url, _, _ := SignRevisionURL(r.Context(), svc, requireUser(r), rev.ID)
	common.ReplyOK(w, map[string]any{
		"artifact_id": art.ID, "revision_id": rev.ID, "revision_no": rev.RevisionNo,
		"content_type": rev.ContentType, "inline_json": rev.InlineJSON, "url": url,
		"content_hash": rev.ContentHash, "size": rev.Size, "caption": rev.Caption,
	})
}

func MoveHeadHTTP(w http.ResponseWriter, r *http.Request) {
	if !Enabled() {
		common.ReplyErr(w, ErrNotFound.Error(), http.StatusNotFound)
		return
	}
	svc := serviceFromStore()
	if svc == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	var body struct {
		RevisionID string `json:"revision_id"`
		Version    int64  `json:"version"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || strings.TrimSpace(body.RevisionID) == "" {
		common.ReplyErr(w, "revision_id is required", http.StatusBadRequest)
		return
	}
	channel := common.PathVar(r, "channel")
	if channel == "" {
		channel = ChannelPublished
	}
	artifactID := common.PathVar(r, "id")
	userID := requireUser(r)
	var (
		head *orm.ArtifactHead
		err  error
	)
	if channel == ChannelPublished {
		head, err = svc.RestorePublished(r.Context(), userID, artifactID, body.RevisionID, body.Version)
	} else {
		head, err = svc.MoveHead(r.Context(), userID, artifactID, channel, body.RevisionID, body.Version)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	common.ReplyOK(w, head)
}

func DownloadURLHTTP(w http.ResponseWriter, r *http.Request) {
	if !Enabled() {
		common.ReplyErr(w, ErrNotFound.Error(), http.StatusNotFound)
		return
	}
	svc := serviceFromStore()
	if svc == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	url, rev, err := SignRevisionURL(r.Context(), svc, requireUser(r), common.PathVar(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	payload := map[string]any{"url": url, "revision_id": common.PathVar(r, "id")}
	if rev != nil && len(rev.InlineJSON) > 0 {
		payload["inline_json"] = json.RawMessage(rev.InlineJSON)
	}
	common.ReplyOK(w, payload)
}

func DiffHTTP(w http.ResponseWriter, r *http.Request) {
	if !Enabled() {
		common.ReplyErr(w, ErrNotFound.Error(), http.StatusNotFound)
		return
	}
	svc := serviceFromStore()
	if svc == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	leftID := r.URL.Query().Get("from")
	rightID := r.URL.Query().Get("to")
	userID := requireUser(r)
	left, _, err := svc.GetRevision(r.Context(), userID, leftID)
	if err != nil {
		writeErr(w, err)
		return
	}
	right, _, err := svc.GetRevision(r.Context(), userID, rightID)
	if err != nil {
		writeErr(w, err)
		return
	}
	leftText, leftOK := revisionText(left)
	rightText, rightOK := revisionText(right)
	if !leftOK || !rightOK || left.Size > 512*1024 || right.Size > 512*1024 {
		common.ReplyOK(w, map[string]any{"comparable": false})
		return
	}
	common.ReplyOK(w, map[string]any{"comparable": true, "from": leftText, "to": rightText})
}

func revisionText(rev *orm.ArtifactRevision) (string, bool) {
	if rev == nil {
		return "", false
	}
	ct := strings.ToLower(rev.ContentType)
	if !strings.Contains(ct, "json") && !strings.Contains(ct, "text") && !strings.Contains(ct, "markdown") {
		return "", false
	}
	if len(rev.InlineJSON) > 0 {
		return string(rev.InlineJSON), true
	}
	return "", false
}

func AuditHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" || userID == "0" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	report, err := AuditLegacy(r.Context(), store.DB(), userID)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, report)
}

type LegacyProjection struct {
	V2ArtifactID  string
	RevisionID    string
	RevisionNo    int64
	Count         int
	LogicalKey    string
	ChangeSummary string
	HeadVersion   int64
	ContentType   string
	Filename      string
	Caption       *string
	InlineJSON    json.RawMessage
	OverlayValue  json.RawMessage
}

func EnrichLegacyDTO(ctx context.Context, svc *Service, userID string, artifactID string) LegacyProjection {
	return EnrichLegacyDTOByBinding(ctx, svc, userID, ScopeLegacyRow, artifactID)
}

// EnrichLegacyDTOByBinding resolves a V2 projection through the producer's
// explicit legacy binding. Main-chat rows and SubAgent rows use different
// scopes, so callers must never infer one from the other.
func EnrichLegacyDTOByBinding(
	ctx context.Context, svc *Service, userID, scopeType, legacyID string,
) LegacyProjection {
	fallback := LegacyProjection{RevisionID: legacyID, RevisionNo: 1, Count: 1}
	if svc == nil || !Enabled() {
		return fallback
	}
	binding, err := svc.FindLatestLegacyBinding(ctx, scopeType, legacyID)
	if err != nil {
		return fallback
	}
	head, _ := svc.Head(ctx, binding.ArtifactID, ChannelPublished)
	headVersion := int64(0)
	if head != nil {
		headVersion = head.Version
	}
	targetRevisionID := binding.RevisionID
	if binding.FollowHead || strings.TrimSpace(targetRevisionID) == "" {
		targetRevisionID = ""
		if head != nil {
			targetRevisionID = head.RevisionID
		}
	}
	current, art, err := svc.GetRevision(ctx, userID, targetRevisionID)
	if err != nil || current.ArtifactID != binding.ArtifactID {
		return fallback
	}
	var count int64
	if err := svc.DB.WithContext(ctx).Model(&orm.ArtifactRevision{}).Where("artifact_id = ?", art.ID).Count(&count).Error; err != nil {
		return fallback
	}
	changeSummary := ""
	filename := art.Title
	if len(current.Metadata) > 0 {
		var meta map[string]any
		if json.Unmarshal(current.Metadata, &meta) == nil {
			if summary, ok := meta["change_summary"].(string); ok {
				changeSummary = summary
			}
			if name, ok := meta["filename"].(string); ok && strings.TrimSpace(name) != "" {
				filename = name
			}
		}
	}
	overlay := json.RawMessage(nil)
	if len(current.InlineJSON) == 0 && current.BlobID != "" {
		if url, _, err := SignRevisionURL(ctx, svc, userID, current.ID); err == nil && url != "" {
			overlay, _ = json.Marshal(map[string]any{"url": url, "filename": filename})
		}
	}
	return LegacyProjection{
		V2ArtifactID: art.ID, RevisionID: current.ID, RevisionNo: current.RevisionNo,
		Count: int(count), LogicalKey: DisplayLogicalKey(art.LogicalKey), ChangeSummary: changeSummary,
		HeadVersion: headVersion, ContentType: current.ContentType, Filename: filename,
		Caption: current.Caption, InlineJSON: current.InlineJSON, OverlayValue: overlay,
	}
}
