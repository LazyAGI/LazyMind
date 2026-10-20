package documentdirectory

import (
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"sort"
)

func currentNodeFormat(state json.RawMessage) bool {
	var value struct {
		Version int `json:"node_format_version"`
	}
	return json.Unmarshal(state, &value) == nil && value.Version == 2
}

// A pending row retains the original observation until all manifests can decide
// membership. Per-inventory copies use stable IDs and never enter the idle queue.
func saveScopedObservation(tx *gorm.DB, row orm.DocumentObservation, o Observation) error {
	ids := map[string]bool{}
	for _, id := range append(o.InventoryIDs, o.InventoryID) {
		if id != "" {
			ids[id] = true
		}
	}
	if len(ids) > 100 {
		return ErrInput
	}
	o.InventoryID = ""
	o.InventoryIDs = nil
	for id := range ids {
		o.InventoryIDs = append(o.InventoryIDs, id)
	}
	sort.Strings(o.InventoryIDs)
	matched := map[string]bool{}
	pending := false
	for _, id := range o.InventoryIDs {
		v, err := inventory(tx, row.UserID, id)
		if err != nil {
			return err
		}
		if v.ConversationID != row.ConversationID {
			return ErrInput
		}
		var source struct {
			Provider  string `json:"provider"`
			Namespace string `json:"namespace"`
		}
		if json.Unmarshal(v.SourceJSON, &source) != nil {
			return ErrInput
		}
		if source.Provider == "feishu_wiki" {
			source.Provider = "feishu"
		}
		for _, c := range o.Candidates {
			if c.Source != source.Provider || c.Namespace != source.Namespace {
				return ErrInput
			}
		}
		inside, _, err := partitionFolderCandidates(tx, id, o.Candidates)
		if err != nil {
			return err
		}
		for _, c := range inside {
			matched[resourceKey(c)] = true
		}
		if len(inside) > 0 {
			part := o
			part.InventoryID = id
			part.InventoryIDs = []string{id}
			part.Candidates = inside
			copy := row
			copy.ID = key([]string{row.ID, "inventory", id})
			copy.PayloadJSON = encoded(part)
			copy.BatchID = v.BatchID
			if copy.BatchID == "" {
				copy.BatchID = "inv:" + id
			}
			if err = tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"payload_json"})}).Create(&copy).Error; err != nil {
				return err
			}
		}
		pending = pending || v.Status != "complete"
	}
	if pending {
		row.PayloadJSON = encoded(o)
		row.BatchID = "sc:" + row.ID[:30]
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	}
	outside := []Candidate{}
	for _, c := range o.Candidates {
		if !matched[resourceKey(c)] {
			outside = append(outside, c)
		}
	}
	if len(outside) > 0 {
		plain := o
		plain.InventoryIDs = nil
		plain.Candidates = outside
		copy := row
		copy.ID = key([]string{row.ID, "unscoped"})
		copy.CallID = key([]string{row.CallID, "unscoped"})
		copy.BatchID = ""
		copy.PayloadJSON = encoded(plain)
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&copy).Error; err != nil {
			return err
		}
	}
	return tx.Where("id=? AND batch_id LIKE ?", row.ID, "sc:%").Delete(&orm.DocumentObservation{}).Error
}
