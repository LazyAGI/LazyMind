package executor

import (
	"context"
	"encoding/json"
	"sort"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
)

const frozenInputsKey = "workflow_control_inputs_frozen"

// FreezeControlledInputs records the ordered, revision-bound input snapshot in
// the existing outbox. Caller holds the session lock in the grant transaction.
// Small text/JSON values are included so an external model sees reviewed edits
// in its actual contract; large values and files retain their exact read refs.
func FreezeControlledInputs(ctx context.Context, db *gorm.DB, attemptID string) error {
	value, err := (DBContextLoader{DB: db}).LoadAttemptContext(ctx, attemptID)
	if err != nil {
		return err
	}
	if value.Metadata[frozenInputsKey] == "true" {
		return nil
	}
	var session orm.WorkflowSession
	if err := db.WithContext(ctx).Where("id = ?", value.SessionID).First(&session).Error; err != nil {
		return err
	}
	if !controlstore.Controlled(session) {
		return nil
	}
	materials := make([]string, 0, len(value.Inputs))
	for material := range value.Inputs {
		materials = append(materials, material)
	}
	sort.Strings(materials)
	remaining := 64 << 10
	for _, material := range materials {
		var items []map[string]any
		list := false
		switch input := value.Inputs[material].(type) {
		case map[string]any:
			items = []map[string]any{input}
		case []map[string]any:
			items = input
			list = true
		case []any:
			for _, raw := range input {
				if item, ok := raw.(map[string]any); ok {
					items = append(items, item)
				}
			}
			list = true
		default:
			continue
		}
		positions := map[int]int{}
		if list {
			var order orm.WorkflowSlotOrder
			if err := db.WithContext(ctx).Where("session_id = ? AND slot_id = ?", session.ID, material).Find(&order).Error; err != nil {
				return err
			}
			if len(order.OrderList) > 0 {
				var indices []int
				if err := json.Unmarshal(order.OrderList, &indices); err != nil {
					return err
				}
				for position, index := range indices {
					positions[index] = position + 1
				}
			}
		}
		for _, item := range items {
			if item["source_type"] != "artifact" {
				continue
			}
			var revision orm.WorkflowSlotRevision
			if err := db.WithContext(ctx).Where("id = ? AND session_id = ?", item["source_revision_id"], session.ID).First(&revision).Error; err != nil {
				return err
			}
			if revision.ListIndex != nil {
				position := positions[*revision.ListIndex]
				if position == 0 {
					position = *revision.ListIndex + 1
				}
				item["list_index"], item["sort_order"] = *revision.ListIndex, position
			}
			typ := value.DeclaredInputTypes[material]
			if typ != "text" && typ != "json" {
				continue
			}
			raw, err := controlstore.ResolveValue(db.WithContext(ctx), revision)
			if err != nil {
				return err
			}
			if len(raw) > 16<<10 || len(raw) > remaining {
				continue
			}
			var decoded any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return err
			}
			if carrier, ok := decoded.(map[string]any); ok {
				if carrier["path"] != nil || carrier["storage"] != nil || carrier["content_base64"] != nil {
					continue
				}
			}
			item["value"], item["value_hash"] = decoded, controlstore.Hash(raw)
			remaining -= len(raw)
		}
		if list {
			sort.SliceStable(items, func(i, j int) bool {
				left, _ := items[i]["sort_order"].(int)
				right, _ := items[j]["sort_order"].(int)
				return left < right
			})
			value.Inputs[material] = items
		}
	}
	value.Metadata[frozenInputsKey] = "true"
	value.Instruction += "\nUse the bound inputs in this contract. An input's value is its exact current revision and overrides earlier conversation text. Preserve list order. When value is absent, read the exact source_revision_id or source_id before executing; never substitute a remembered older version."
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Model(&orm.WorkflowOutbox{}).Where("attempt_id = ?", attemptID).Update("payload_json", body).Error
}
