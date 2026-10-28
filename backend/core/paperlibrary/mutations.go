package paperlibrary

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"strings"
	"time"
)

var ErrConflict = errors.New("论文已在其他窗口更新，请保留草稿并加载最新内容后重试")

func Entry(tx *gorm.DB, dataset, id string, revision int64) (orm.PaperLibraryEntry, error) {
	var row orm.PaperLibraryEntry
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=? AND id=? AND removed_at IS NULL", dataset, id).Take(&row).Error
	if err != nil {
		return row, err
	}
	if row.Revision != revision {
		return row, ErrConflict
	}
	return row, nil
}
func SaveEntry(tx *gorm.DB, row *orm.PaperLibraryEntry) error {
	previous := row.Revision
	row.Revision++
	row.UpdatedAt = time.Now().UTC()
	result := tx.Model(&orm.PaperLibraryEntry{}).Where("dataset_id=? AND id=? AND revision=?", row.DatasetID, row.ID, previous).Select("academic_work_id", "folder_id", "status", "tags_json", "metadata_json", "source", "import_method", "source_url", "arxiv_version", "revision", "removed_at", "updated_at").Updates(row)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}
func CheckFolder(tx *gorm.DB, dataset, id string) error {
	if id == "" {
		return nil
	}
	var row orm.PaperLibraryFolder
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=? AND id=?", dataset, id).Take(&row).Error
}
func ValidID(id string) bool { return strings.TrimSpace(id) != "" && len(id) <= 255 }
