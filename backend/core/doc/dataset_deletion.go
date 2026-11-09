package doc

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"net/http"
	"net/url"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/store"
)

type DatasetDeletionProgress struct {
	DisplayName string `json:"display_name"`
	DatasetID   string `json:"dataset_id"`
	Status      string `json:"status"`
	Total       int    `json:"total"`
	Completed   int    `json:"completed"`
	Failed      int64  `json:"failed"`
	Error       string `json:"error,omitempty"`
}

// The deletion receipt survives the dataset's soft deletion. Only its original owner may inspect it.
func GetDatasetDeletion(w http.ResponseWriter, r *http.Request) {
	caller := datasetCatalogCallerFromRequest(r)
	var ds orm.Dataset
	if caller.UserID == "" || store.DB().WithContext(r.Context()).Where("id = ? AND create_user_id = ? AND deleted_at IS NOT NULL", datasetIDFromPath(r), caller.UserID).Take(&ds).Error != nil || (ds.TenantID != "" && resourceTenant(ds.TenantID) != resourceTenant(caller.TenantID)) {
		common.ReplyErr(w, "dataset not found", http.StatusNotFound)
		return
	}
	var receipt orm.ResourceOperationReceipt
	if err := store.DB().WithContext(r.Context()).Where("tenant_id = ? AND user_id = ? AND operation_key = ?", resourceTenant(caller.TenantID), caller.UserID, "dataset-delete:"+ds.ID).Take(&receipt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "dataset not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query tasks failed", http.StatusInternalServerError)
		return
	}
	var result ResourceOperationResult
	if err := json.Unmarshal(receipt.Result, &result); err != nil || len(result.Items) != 1 {
		common.ReplyErr(w, "query tasks failed", http.StatusInternalServerError)
		return
	}
	item := result.Items[0]
	// Legacy official datasets have no tenant; the scoped deletion receipt proves the tenant of this operation.
	if receipt.Action != "knowledge.resource.delete" || item.ResourceRef.Kind != "knowledge_base" || item.ResourceRef.KnowledgeBaseID != ds.ID {
		common.ReplyErr(w, "dataset not found", http.StatusNotFound)
		return
	}
	progress := DatasetDeletionProgress{DisplayName: ds.DisplayName, DatasetID: ds.ID, Status: "cleaning", Total: item.DocumentCount}
	if item.CleanupStatus != "submitted" || item.Error != nil {
		progress.Status = "failed"
		progress.Error = "cleanup was not submitted"
		if item.Error != nil {
			progress.Error = item.Error.Message
		}
		common.ReplyJSON(w, progress)
		return
	}
	kbID := ds.KbID
	if kbID == "" {
		kbID = ds.ID
	}
	var kb struct {
		Code int `json:"code"`
		Data *struct {
			Status   string `json:"status"`
			DocCount int    `json:"doc_count"`
		} `json:"data"`
	}
	err := common.ApiGet(r.Context(), common.JoinURL(common.AlgoServiceEndpoint(), "/v1/kbs/"+url.PathEscape(kbID)), nil, &kb, 10*time.Second)
	var httpErr *common.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		progress.Status, progress.Completed = "completed", progress.Total
		common.ReplyJSON(w, progress)
		return
	}
	if err != nil || kb.Code != 200 || kb.Data == nil {
		common.ReplyErr(w, "query tasks failed", http.StatusBadGateway)
		return
	}
	if kb.Data.Status == "DELETED" {
		progress.Status, progress.Completed = "completed", progress.Total
	} else if kb.Data.Status != "DELETING" {
		progress.Status, progress.Error = "failed", "cleanup was not started"
	} else {
		progress.Total = max(progress.Total, kb.Data.DocCount)
		progress.Completed = max(0, progress.Total-kb.Data.DocCount)
		lazyDB := store.LazyLLMDB().WithContext(r.Context())
		tasks := lazyDB.Model(&readonlyorm.LazyLLMDocServiceTaskRow{}).Select("task_id").Where("kb_id = ? AND task_type = ?", kbID, "DOC_DELETE")
		if err := lazyDB.Model(&readonlyorm.LazyLLMDocParseStateRow{}).Where("kb_id = ? AND status IN ? AND current_task_id IN (?)", kbID, []string{"FAILED", "CANCELED"}, tasks).Count(&progress.Failed).Error; err != nil {
			common.ReplyErr(w, "query tasks failed", http.StatusInternalServerError)
			return
		}
		if progress.Failed > 0 {
			progress.Status, progress.Error = "failed", "document cleanup failed"
		}
	}
	common.ReplyJSON(w, progress)
}
