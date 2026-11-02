package academic

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// ResolveExpansionPapers provides frozen identities and content metadata to
// publishers. No model-authored metadata or full body is accepted or returned.
func ResolveExpansionPapers(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	record, ok := ownedExpansion(w, r, user, mux.Vars(r)["expansion_id"])
	if !ok {
		return
	}
	var req struct {
		PaperIDs []string `json:"paper_ids"`
	}
	if !parseJSON(r, &req) || req.PaperIDs == nil || len(req.PaperIDs) > 500 {
		replyError(w, 400, "INVALID_REQUEST", "paper_ids must be an array of at most 500 registered identities")
		return
	}
	papers := make([]WorkflowPaper, 0, len(req.PaperIDs))
	contents := make([]map[string]any, 0, len(req.PaperIDs))
	seen := map[string]bool{}
	db := store.DB().WithContext(r.Context())
	for _, id := range req.PaperIDs {
		if strings.TrimSpace(id) == "" || seen[id] {
			replyError(w, 400, "INVALID_REQUEST", "paper_ids must be nonempty and unique")
			return
		}
		seen[id] = true
		source, err := expansionSource(db, record, id)
		if err != nil {
			replyError(w, 422, "UNREGISTERED_EXPANSION_PAPER", err.Error())
			return
		}
		var mark orm.AcademicUserPaperState
		if err := db.Where("user_id=? AND dataset_id=? AND paper_id=?", user, record.DatasetID, id).Take(&mark).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			replyError(w, 500, "READ_FAILED", err.Error())
			return
		}
		papers = append(papers, WorkflowPaper{Paper: source.Paper, IsSeed: source.IsSeed, IsImportant: mark.IsImportant})
		state := map[string]any{"paper_id": id, "status": "unavailable", "source_version": source.Paper.SourceVersion}
		if content, err := findSourceContent(db, user, source); err == nil {
			state["status"], state["source_kind"], state["source_version"] = content.Status, content.SourceKind, content.SourceVersion
			state["error"] = content.Error
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			replyError(w, 500, "READ_FAILED", err.Error())
			return
		}
		contents = append(contents, state)
	}
	common.ReplyJSON(w, map[string]any{"papers": papers, "contents": contents})
}
