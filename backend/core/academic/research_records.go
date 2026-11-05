package academic

import (
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

type academicMaterialProgress struct {
	ProcessingCount    int  `json:"processing_count"`
	DistinctReadyCount *int `json:"distinct_ready_count,omitempty"`
}

type academicExpansionRecord struct {
	orm.AcademicExpansion
	MaterialProgress *academicMaterialProgress `json:"material_progress,omitempty"`
}

func readExpansionContents(db *gorm.DB, record orm.AcademicExpansion, ids []string) ([]orm.AcademicPaperContent, error) {
	contents := make([]orm.AcademicPaperContent, 0, len(ids))
	for _, id := range ids {
		source, err := expansionSource(db, record, id)
		if err != nil {
			continue
		}
		content, err := findSourceContent(db, record.UserID, source)
		if err == nil {
			contents = append(contents, content)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	return contents, nil
}

func summarizeExpansionContents(contents []orm.AcademicPaperContent) academicMaterialProgress {
	var result academicMaterialProgress
	ready := make(map[[2]string]bool)
	for _, content := range contents {
		if paperContentProcessing(content) {
			result.ProcessingCount++
		}
		if usablePaperContent(content) {
			ready[[2]string{content.SourceHash, content.ContentHash}] = true
		}
	}
	count := len(ready)
	result.DistinctReadyCount = &count
	return result
}

func listExpansionRecords(db *gorm.DB, user string, expansions []orm.AcademicExpansion) ([]academicExpansionRecord, error) {
	// Only papers with active content can change an expansion's completed badge.
	// Resolve exact sources for those papers, and count eligible papers for
	// reviews still waiting to hand off.
	var activeIDs []string
	if err := db.Model(&orm.AcademicPaperContent{}).
		Where("user_id=? AND parser_version=? AND status IN ?", user, orm.AcademicPaperParserVersion, []string{"pending", "processing"}).
		Distinct("paper_id").Pluck("paper_id", &activeIDs).Error; err != nil {
		return nil, err
	}
	active := make(map[string]bool, len(activeIDs))
	for _, id := range activeIDs {
		active[id] = true
	}
	records := make([]academicExpansionRecord, 0, len(expansions))
	for _, expansion := range expansions {
		record := academicExpansionRecord{AcademicExpansion: expansion}
		if expansion.Status == "completed" {
			progress := academicMaterialProgress{}
			reviewPending := expansion.Goal == "review" && expansion.AutoReviewID == ""
			if reviewPending || expansion.Goal != "review" && len(active) > 0 {
				var input ReviewWorkflowInput
				if err := json.Unmarshal(expansion.ResultJSON, &input); err != nil {
					return nil, err
				}
				ids := make([]string, 0, len(input.Papers))
				for _, item := range input.Papers {
					if reviewPending || active[item.Paper.PaperID] {
						ids = append(ids, item.Paper.PaperID)
					}
				}
				contents, err := readExpansionContents(db, expansion, ids)
				if err != nil {
					return nil, err
				}
				progress = summarizeExpansionContents(contents)
				if !reviewPending {
					progress.DistinctReadyCount = nil
				}
			}
			record.MaterialProgress = &progress
		}
		records = append(records, record)
	}
	return records, nil
}

// Review records are views of existing handoffs, not a second task history.
type academicReviewRecord struct {
	ID              string    `json:"id"`
	ConversationID  string    `json:"conversation_id"`
	ExpansionID     string    `json:"expansion_id"`
	Version         int64     `json:"version"`
	Title           string    `json:"title"`
	PaperCount      int       `json:"paper_count"`
	SourceTitle     string    `json:"source_title"`
	SourceAvailable bool      `json:"source_available"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func listReviewRecords(db *gorm.DB, user, dataset string, expansions []orm.AcademicExpansion) ([]academicReviewRecord, error) {
	var rows []struct {
		Record    academicReviewRecord `gorm:"embedded"`
		InputJSON json.RawMessage
	}
	err := db.Table("academic_handoffs h").
		Select("h.id, h.conversation_id, h.expansion_id, h.version, h.created_at, h.input_json, c.display_name AS title, c.updated_at").
		Joins("JOIN conversations c ON c.id=h.conversation_id AND c.create_user_id=h.user_id AND c.deleted_at IS NULL").
		Where("h.user_id=? AND h.dataset_id=? AND h.kind=?", user, dataset, "review").
		Order("h.created_at DESC, h.id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	sources := make(map[string]orm.AcademicExpansion, len(expansions))
	for _, expansion := range expansions {
		sources[expansion.ID] = expansion
	}
	records := make([]academicReviewRecord, 0, len(rows))
	for _, row := range rows {
		var input ReviewWorkflowInput
		if err := json.Unmarshal(row.InputJSON, &input); err != nil {
			return nil, err
		}
		record := row.Record
		record.PaperCount = len(input.Papers)
		if source, ok := sources[record.ExpansionID]; ok {
			record.SourceTitle, record.SourceAvailable = source.Title, true
		}
		if record.Title == "" {
			record.Title = paperTaskNames["review"]
		}
		if record.UpdatedAt.Before(record.CreatedAt) {
			record.UpdatedAt = record.CreatedAt
		}
		records = append(records, record)
	}
	return records, nil
}
