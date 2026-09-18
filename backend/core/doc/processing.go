package doc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelprovider"
	"lazymind/core/store"
)

const (
	ProcessingLevelStored  = "stored"
	ProcessingLevelParsed  = "parsed"
	ProcessingLevelChunked = "chunked"
	ProcessingLevelIndexed = "indexed"
	TransitionIdle         = "idle"
)

type DatasetCapabilities struct {
	List     bool `json:"list"`
	Read     bool `json:"read"`
	Search   bool `json:"search"`
	Retrieve bool `json:"retrieve"`
}

type ProcessingCoverage struct {
	Available  int64 `json:"available_documents"`
	Total      int64 `json:"total_documents"`
	Failed     int64 `json:"failed_documents"`
	Processing int64 `json:"processing_documents"`
}

func normalizeProcessingLevel(level string) (string, error) {
	level = strings.ToLower(strings.TrimSpace(level))
	if level == "" {
		return ProcessingLevelIndexed, nil
	}
	switch level {
	case ProcessingLevelStored, ProcessingLevelParsed, ProcessingLevelChunked, ProcessingLevelIndexed:
		return level, nil
	default:
		return "", fmt.Errorf("processing_level must be stored, parsed, chunked, or indexed")
	}
}

func effectiveProcessingLevel(level string) string {
	normalized, err := normalizeProcessingLevel(level)
	if err != nil {
		return ProcessingLevelIndexed
	}
	return normalized
}

func effectiveTransitionStatus(status string) string {
	if strings.TrimSpace(status) == "" {
		return TransitionIdle
	}
	return status
}

func capabilitiesForProcessingLevel(level string) DatasetCapabilities {
	level = effectiveProcessingLevel(level)
	return DatasetCapabilities{List: true, Read: true,
		Search:   level == ProcessingLevelChunked || level == ProcessingLevelIndexed,
		Retrieve: level == ProcessingLevelIndexed}
}

func datasetRequiresEmbedding(ds *orm.Dataset) bool {
	return ds == nil || effectiveProcessingLevel(ds.ProcessingLevel) == ProcessingLevelIndexed
}

func newDocumentProcessingState(datasetID, documentID string, now time.Time) orm.DocumentProcessingState {
	return orm.DocumentProcessingState{DatasetID: datasetID, DocumentID: documentID,
		ParseStatus: "pending", ChunkStatus: "pending", IndexStatus: "pending",
		Revision: 1, UpdatedAt: now}
}

func createDocumentProcessingState(db *gorm.DB, datasetID, documentID string, now time.Time) error {
	if !db.Migrator().HasTable(&orm.DocumentProcessingState{}) {
		return nil
	}
	state := newDocumentProcessingState(datasetID, documentID, now)
	return db.Create(&state).Error
}

type processingPreflightRequest struct {
	ProcessingLevel        string `json:"processing_level"`
	ReaderFallbackAccepted bool   `json:"reader_fallback_accepted"`
}
type processingIssue struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Fallback string `json:"fallback,omitempty"`
}
type processingPreflightResponse struct {
	Allowed  bool              `json:"allowed"`
	Blocking []processingIssue `json:"blocking"`
	Warnings []processingIssue `json:"warnings"`
}

func ProcessingPreflight(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	var req processingPreflightRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	level, err := normalizeProcessingLevel(req.ProcessingLevel)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp := processingPreflightResponse{Allowed: true, Blocking: []processingIssue{}, Warnings: []processingIssue{}}
	if level != ProcessingLevelStored && !req.ReaderFallbackAccepted {
		resp.Warnings = append(resp.Warnings, processingIssue{Code: "OCR_PROVIDER_NOT_CONFIGURED", Fallback: "builtin_pdf_reader", Message: "Scanned or image-only PDFs may not yield reliable text."})
	}
	if level == ProcessingLevelIndexed {
		ready, checkErr := modelprovider.IsModelReady(r.Context(), store.DB(), userID, "embed_main")
		if checkErr != nil {
			common.ReplyErr(w, "algorithm service unavailable: cannot check embedding model", http.StatusBadGateway)
			return
		}
		if !ready {
			resp.Allowed = false
			resp.Blocking = append(resp.Blocking, processingIssue{Code: "EMBEDDING_MODEL_NOT_READY", Message: "Embedding model is required for indexed knowledge bases."})
		}
	}
	common.ReplyJSON(w, resp)
}

type updateProcessingLevelRequest struct {
	ProcessingLevel        string `json:"processing_level"`
	ReaderFallbackAccepted *bool  `json:"reader_fallback_accepted,omitempty"`
}

func UpdateProcessingLevel(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	ds, userID, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetWrite)
	if !ok {
		if userID == "" {
			common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		} else {
			replyDatasetForbidden(w)
		}
		return
	}
	var req updateProcessingLevelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	level, err := normalizeProcessingLevel(req.ProcessingLevel)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if level == ProcessingLevelIndexed && replyEmbedNotReady(w, r, userID) {
		return
	}
	updates := map[string]any{"processing_level": level, "processing_revision": ds.ProcessingRevision + 1, "transition_status": TransitionIdle, "updated_at": time.Now().UTC()}
	if req.ReaderFallbackAccepted != nil {
		updates["reader_fallback_accepted"] = *req.ReaderFallbackAccepted
	}
	if err := store.DB().WithContext(r.Context()).Model(&orm.Dataset{}).Where("id = ?", datasetID).Updates(updates).Error; err != nil {
		common.ReplyErr(w, "update processing level failed", http.StatusInternalServerError)
		return
	}
	common.ReplyJSON(w, map[string]any{"dataset_id": datasetID, "processing_level": level, "processing_revision": ds.ProcessingRevision + 1, "transition_status": TransitionIdle, "capabilities": capabilitiesForProcessingLevel(level)})
}

func GetProcessingStatus(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	ds, userID, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetRead)
	if !ok {
		if userID == "" {
			common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		} else {
			replyDatasetForbidden(w)
		}
		return
	}
	var total, failed, processing, available int64
	db := store.DB().WithContext(r.Context())
	_ = db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", datasetID).Count(&total).Error
	_ = db.Model(&orm.DocumentProcessingState{}).Where("dataset_id = ? AND (parse_status = 'failed' OR chunk_status = 'failed' OR index_status = 'failed')", datasetID).Count(&failed).Error
	_ = db.Model(&orm.DocumentProcessingState{}).Where("dataset_id = ? AND (parse_status = 'running' OR chunk_status = 'running' OR index_status = 'running')", datasetID).Count(&processing).Error
	level := effectiveProcessingLevel(ds.ProcessingLevel)
	statusColumn := map[string]string{ProcessingLevelParsed: "parse_status", ProcessingLevelChunked: "chunk_status", ProcessingLevelIndexed: "index_status"}[level]
	if level == ProcessingLevelStored {
		available = total
	} else if statusColumn != "" {
		_ = db.Model(&orm.DocumentProcessingState{}).Where("dataset_id = ? AND "+statusColumn+" = 'succeeded'", datasetID).Count(&available).Error
	}
	common.ReplyJSON(w, map[string]any{"dataset_id": datasetID, "processing_level": level, "processing_revision": ds.ProcessingRevision, "transition_status": effectiveTransitionStatus(ds.TransitionStatus), "capabilities": capabilitiesForProcessingLevel(level), "coverage": ProcessingCoverage{Available: available, Total: total, Failed: failed, Processing: processing}})
}
