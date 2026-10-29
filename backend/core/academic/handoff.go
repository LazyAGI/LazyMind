package academic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type PaperTaskNote struct {
	ID      string `json:"id"`
	PaperID string `json:"paper_id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// Reading notes and presentations consume the original paper list, without
// the research-direction and relationship fields used by a literature review.
type PaperTaskInput struct {
	UserInstruction string          `json:"user_instruction"`
	Papers          []PaperRef      `json:"papers"`
	Notes           []PaperTaskNote `json:"notes,omitempty"`
}

type academicChatSummary struct {
	Kind                 string `json:"kind"`
	DatasetID            string `json:"dataset_id"`
	Status               string `json:"status"`
	Version              int64  `json:"version"`
	HandoffID            string `json:"handoff_id,omitempty"`
	ExpansionID          string `json:"expansion_id,omitempty"`
	NextConversationID   string `json:"next_conversation_id,omitempty"`
	SourceConversationID string `json:"source_conversation_id,omitempty"`
	SourceAvailable      bool   `json:"source_available,omitempty"`
	ReadyCount           int    `json:"ready_count,omitempty"`
	PaperCount           int    `json:"paper_count,omitempty"`
}

type academicHandoffContext struct {
	academicChatSummary
	Input json.RawMessage `json:"input"`
}

type academicHandoffLink struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	ConversationID string `json:"conversation_id"`
	Version        int64  `json:"version"`
}

type handoffContentSnapshot struct {
	Sources  []paperSource              `json:"sources"`
	Contents []orm.AcademicPaperContent `json:"contents"`
}

var paperTaskNames = map[string]string{"expansion": "论文扩展", "review": "论文综述", "reflection": "阅读心得", "ppt": "汇报 PPT"}

func paperTaskNotes(ctx context.Context, db *gorm.DB, dataset string, papers []PaperRef) ([]PaperTaskNote, error) {
	ids := []string{}
	for _, p := range papers {
		ids = append(ids, p.PaperID)
	}
	notes := []PaperTaskNote{}
	if len(ids) == 0 {
		return notes, nil
	}
	err := db.WithContext(ctx).Table("paper_library_notes n").Select("n.id,e.academic_work_id AS paper_id,n.title,n.content").Joins("JOIN paper_library_entries e ON e.id=n.paper_id AND e.dataset_id=n.dataset_id AND e.removed_at IS NULL").Where("e.dataset_id=? AND e.academic_work_id IN ?", dataset, ids).Order("n.created_at,n.id").Scan(&notes).Error
	return notes, err
}
func handoffPapers(handoff orm.AcademicHandoff) ([]PaperRef, error) {
	papers := []PaperRef{}
	if handoff.Kind == "review" {
		var input ReviewWorkflowInput
		if err := json.Unmarshal(handoff.InputJSON, &input); err != nil {
			return nil, err
		}
		for _, p := range input.Papers {
			papers = append(papers, p.Paper)
		}
	} else {
		var input PaperTaskInput
		if err := json.Unmarshal(handoff.InputJSON, &input); err != nil {
			return nil, err
		}
		papers = input.Papers
	}
	for _, p := range papers {
		if p.PaperID == "" {
			return nil, errors.New("handoff paper identity is missing")
		}
	}
	return papers, nil
}

func ownedHandoff(w http.ResponseWriter, r *http.Request, user, id string) (orm.AcademicHandoff, bool) {
	var handoff orm.AcademicHandoff
	if store.DB().Where("id=? AND user_id=?", id, user).Take(&handoff).Error != nil {
		replyError(w, 404, "NOT_FOUND", "handoff not found")
		return handoff, false
	}
	var conversation orm.Conversation
	if store.DB().Where("id=? AND create_user_id=? AND deleted_at IS NULL", handoff.ConversationID, user).Take(&conversation).Error != nil || !academicDataset(r, user, handoff.DatasetID) {
		replyError(w, 403, "FORBIDDEN", "paper task unavailable")
		return handoff, false
	}
	return handoff, true
}

func handoffSource(db *gorm.DB, handoff orm.AcademicHandoff, paperID string) (paperSource, []orm.AcademicPaperContent, error) {
	var snapshot handoffContentSnapshot
	if err := json.Unmarshal(handoff.ContentsJSON, &snapshot); err != nil {
		return paperSource{}, nil, err
	}
	for _, source := range snapshot.Sources {
		if source.Paper.PaperID == paperID {
			if source.DocumentID != "" {
				if _, err := sourceDocument(db.Statement.Context, db, source); err != nil {
					return paperSource{}, nil, err
				}
			}
			return source, snapshot.Contents, nil
		}
	}
	return paperSource{}, nil, errors.New("paper has no frozen source in this handoff")
}
