package academic

import (
	"errors"
	"net/http"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/store"
)

type reviewMaterial struct {
	PaperID  string `json:"paper_id"`
	Status   string `json:"status"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"`
}

// Resolve only the selected library materials. Direct references belong to the
// downstream review scope, and must not launch a discovery/expansion task.
func reviewSelectionMaterials(r *http.Request, user, dataset string, papers []WorkflowPaper) ([]reviewMaterial, error) {
	result := make([]reviewMaterial, 0, len(papers))
	sources, err := seedSources(store.DB().WithContext(r.Context()), dataset, papers)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		item := reviewMaterial{PaperID: source.Paper.PaperID}
		document, err := sourceDocument(r.Context(), store.DB(), source)
		if err != nil {
			return nil, err
		}
		status, reason, err := paperProcessingStatus(r.Context(), store.DB(), document)
		if err != nil {
			return nil, err
		}
		if status != "ready" {
			item.Status, item.Reason = status, firstNonEmpty(reason, "论文尚未解析完成")
		} else {
			content, err := ensurePaperContent(r.Context(), store.DB(), user, source.Paper.PaperID, source)
			if err != nil {
				return nil, err
			}
			item.Status, item.Eligible = content.Status, usablePaperContent(content)
			if !item.Eligible {
				item.Reason = firstNonEmpty(content.Error, "全文尚不可用")
				if content.Status == "processing" {
					item.Status, item.Reason = "preparing", "正在准备已有全文"
				}
			}
		}
		result = append(result, item)
	}
	return result, nil
}

func launchSelectedReview(w http.ResponseWriter, r *http.Request, user string, req paperLaunch, input ReviewWorkflowInput) {
	// Retrying an accepted launch must return its original conversation, even if
	// the source library has changed since the request was accepted.
	var existing orm.AcademicHandoff
	if err := store.DB().Where("user_id=? AND request_key=?", user, req.RequestKey).Take(&existing).Error; err == nil {
		if existing.Kind != "review" || existing.DatasetID != req.DatasetID || existing.ExpansionID != "" {
			replyError(w, 409, "LAUNCH_FAILED", "request key belongs to another task")
			return
		}
		common.ReplyJSON(w, map[string]string{"conversation_id": existing.ConversationID})
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		replyError(w, 500, "LAUNCH_FAILED", err.Error())
		return
	}
	selected, err := selectReviewPapers(input, req.PaperIDs)
	if err != nil {
		replyError(w, 422, "REVIEW_NOT_READY", err.Error())
		return
	}
	materials, err := reviewSelectionMaterials(r, user, req.DatasetID, selected.Papers)
	if err != nil {
		replyError(w, 422, "REVIEW_NOT_READY", err.Error())
		return
	}
	for _, material := range materials {
		if !material.Eligible {
			replyError(w, 422, "REVIEW_NOT_READY", material.Reason)
			return
		}
	}
	selected.ReviewScope = firstNonEmpty(req.ReviewScope, "selected")
	var conversation string
	err = conversationgroup.UserTransaction(r.Context(), store.DB(), user, func(tx *gorm.DB) error {
		snapshot, contents, err := snapshotReview(r.Context(), tx, user, req.DatasetID, selected, true)
		if err != nil {
			return err
		}
		handoff, err := createPaperHandoff(r.Context(), tx, user, req.DatasetID, "", 0, "review", req.RequestKey, snapshot, contents, false)
		conversation = handoff.ConversationID
		return err
	})
	if err != nil {
		replyError(w, 422, "REVIEW_NOT_READY", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]string{"conversation_id": conversation})
}
