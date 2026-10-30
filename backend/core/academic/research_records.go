package academic

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

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
