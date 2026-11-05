package academic

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	workflowstore "lazymind/core/workflow/store"
)

type mockHandoffResult struct {
	Kind               string          `json:"kind"`
	Input              json.RawMessage `json:"input"`
	Status             string          `json:"status"`
	Feedback           string          `json:"feedback"`
	ReflectionMarkdown string          `json:"reflection_markdown,omitempty"`
}

// SubmitMockHandoff is the downstream adapter for this release. It freezes the
// actual request from the owned handoff; a model cannot rewrite the paper set.
func SubmitMockHandoff(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		ReflectionMarkdown string `json:"reflection_markdown"`
	}
	if !parseJSON(r, &req) {
		replyError(w, 400, "INVALID_REQUEST", "invalid mock request")
		return
	}
	var owned orm.AcademicHandoff
	if store.DB().Where("id=? AND user_id=?", mux.Vars(r)["handoff_id"], user).Take(&owned).Error != nil || !academicDataset(r, user, owned.DatasetID) {
		replyError(w, 404, "NOT_FOUND", "handoff not found")
		return
	}
	var output mockHandoffResult
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var handoff orm.AcademicHandoff
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", mux.Vars(r)["handoff_id"], user).Take(&handoff).Error; err != nil {
			return err
		}
		var conversation orm.Conversation
		if err := tx.Where("id=? AND create_user_id=? AND deleted_at IS NULL", handoff.ConversationID, user).Take(&conversation).Error; err != nil {
			return err
		}
		if len(handoff.MockResultJSON) > 2 {
			return json.Unmarshal(handoff.MockResultJSON, &output)
		}
		if _, err := handoffPapers(handoff); err != nil {
			return err
		}
		reflection := ""
		if handoff.ReflectionFirst {
			var session orm.WorkflowSession
			if err := tx.Where("conversation_id=? AND plugin_id='paper_ppt_with_reflection_mock'", handoff.ConversationID).Order("created_at DESC").Take(&session).Error; err != nil {
				return err
			}
			var completed orm.WorkflowSessionStep
			if err := tx.Where("session_id=? AND step_id='reflection' AND status='succeeded' AND validity='effective'", session.ID).Take(&completed).Error; err != nil {
				return errors.New("阅读心得尚未完成")
			}
			artifacts, err := workflowstore.New(tx).ListArtifacts(r.Context(), user, session.ID)
			if err != nil {
				return err
			}
			for _, artifact := range artifacts {
				if artifact.Slot != "reflection_markdown" || artifact.Validity != "effective" || !artifact.Selected || artifact.Deleted {
					continue
				}
				value, err := academicArtifactJSON(artifact.Value)
				if err != nil {
					return err
				}
				var text struct {
					Text string `json:"text"`
				}
				if json.Unmarshal(value, &text) == nil {
					reflection = text.Text
				} else {
					_ = json.Unmarshal(value, &reflection)
				}
			}
			if strings.TrimSpace(reflection) == "" {
				return errors.New("阅读心得尚未完成")
			}
			if req.ReflectionMarkdown != "" && req.ReflectionMarkdown != reflection {
				return errors.New("心得与已保存的上一步产物不一致")
			}
		}
		output = mockHandoffResult{
			Kind: handoff.Kind, Input: handoff.InputJSON,
			Status: "awaiting_downstream", Feedback: "材料已接收，生成能力待对接。",
			ReflectionMarkdown: reflection,
		}
		return tx.Model(&handoff).Update("mock_result_json", marshal(output)).Error
	})
	if err != nil {
		replyError(w, 422, "MOCK_NOT_READY", err.Error())
		return
	}
	common.ReplyJSON(w, output)
}
