package controlstore

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

// PresentationSlots filters current reads and edits by the saved authoring route.
// Older exports remain in revision history and are never inputs to an HTML edit.
func PresentationSlots(db *gorm.DB, rows []orm.WorkflowSlotRevision) ([]orm.WorkflowSlotRevision, error) {
	for _, row := range rows {
		if row.SlotID != "presentation_output_format" || !row.Selected || (row.Validity != "" && row.Validity != "effective") {
			continue
		}
		raw, err := ResolveValue(db, row)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			break
		}
		if err != nil {
			return nil, err
		}
		var choice struct{ Text, Data string }
		if json.Unmarshal(common.CanonicalizeTextArtifactValue("text", raw), &choice) == nil {
			format := strings.TrimSpace(choice.Text)
			if format == "" {
				format = strings.TrimSpace(choice.Data)
			}
			return slices.DeleteFunc(rows, func(item orm.WorkflowSlotRevision) bool {
				return !PresentationSlotAllowed(format, item.SlotID)
			}), nil
		}
		break
	}
	return rows, nil
}

func PresentationSlotAllowed(format, slot string) bool {
	switch format {
	case "html":
		return slot != "native_pptx"
	case "native_pptx":
		return slot != "preview_html" && slot != "preview_notes"
	default:
		return true
	}
}
