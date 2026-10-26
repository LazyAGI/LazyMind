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

type handoffContentSnapshot struct {
	Sources  []paperSource              `json:"sources"`
	Contents []orm.AcademicPaperContent `json:"contents"`
}

var paperTaskNames = map[string]string{"expansion": "论文扩展", "review": "论文综述", "reflection": "阅读心得", "ppt": "汇报 PPT"}

func paperTaskNotes(ctx context.Context, db *gorm.DB, dataset string, papers []PaperRef) ([]PaperTaskNote, error) {
	library, err := readPaperLibrary(ctx, db, dataset)
	if err != nil {
		return nil, err
	}
	var graph struct {
		Papers      []importLibraryPaper `json:"papers"`
		Attachments []struct {
			PaperID    string `json:"paperId"`
			ContentRef string `json:"contentRef"`
		} `json:"attachments"`
		Notes []struct {
			ID      string `json:"id"`
			PaperID string `json:"paperId"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"notes"`
	}
	if err = json.Unmarshal(library.Data, &graph); err != nil {
		return nil, err
	}
	selected, documents, libraryIDs := map[string]bool{}, map[string]string{}, map[string]string{}
	for _, paper := range papers {
		selected[paper.PaperID] = true
		if paper.SourceDocumentID != "" {
			documents[paper.SourceDocumentID] = paper.PaperID
		}
	}
	for _, paper := range graph.Papers {
		if selected[paper.AcademicWorkID] {
			libraryIDs[paper.ID] = paper.AcademicWorkID
		}
	}
	for _, attachment := range graph.Attachments {
		if id := documents[attachment.ContentRef]; id != "" {
			libraryIDs[attachment.PaperID] = id
		}
	}
	notes := []PaperTaskNote{}
	for _, note := range graph.Notes {
		if id := libraryIDs[note.PaperID]; id != "" {
			notes = append(notes, PaperTaskNote{ID: note.ID, PaperID: id, Title: note.Title, Content: note.Content})
		}
	}
	return notes, nil
}

// Legacy handoffs wrapped every paper in WorkflowPaper. Keep them readable
// while new reading-note/PPT handoffs use the agreed list[PaperRef] contract.
func handoffPapers(input json.RawMessage) ([]PaperRef, error) {
	var value struct {
		Papers []json.RawMessage `json:"papers"`
	}
	if err := json.Unmarshal(input, &value); err != nil {
		return nil, err
	}
	papers := make([]PaperRef, 0, len(value.Papers))
	for _, raw := range value.Papers {
		var paper PaperRef
		if err := json.Unmarshal(raw, &paper); err != nil {
			return nil, err
		}
		if paper.PaperID == "" {
			var legacy WorkflowPaper
			if err := json.Unmarshal(raw, &legacy); err != nil {
				return nil, err
			}
			paper = legacy.Paper
		}
		if paper.PaperID == "" {
			return nil, errors.New("handoff paper identity is missing")
		}
		papers = append(papers, paper)
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
		// Previous releases stored only a content-metadata array.
		if err = json.Unmarshal(handoff.ContentsJSON, &snapshot.Contents); err != nil {
			return paperSource{}, nil, err
		}
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
	if len(snapshot.Sources) > 0 {
		return paperSource{}, nil, errors.New("paper is not in this handoff")
	}
	papers, err := handoffPapers(handoff.InputJSON)
	if err != nil {
		return paperSource{}, nil, err
	}
	for _, paper := range papers {
		if paper.PaperID != paperID {
			continue
		}
		if paper.SourceDocumentID != "" {
			sources, err := seedSources(db, handoff.DatasetID, []WorkflowPaper{{Paper: paper, IsSeed: true}})
			if err != nil {
				return paperSource{}, nil, err
			}
			return sources[0], snapshot.Contents, nil
		}
		return paperSource{Paper: paper, DatasetID: handoff.DatasetID}, snapshot.Contents, nil
	}
	return paperSource{}, nil, errors.New("paper is not in this handoff")
}
