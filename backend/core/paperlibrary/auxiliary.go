package paperlibrary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

// UnmarshalJSON preserves extension fields for imports and complete backups.
func (d *Data) UnmarshalJSON(raw []byte) error {
	type plain Data
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	*d = Data(decoded)
	d.Extra = append(json.RawMessage(nil), raw...)
	return nil
}

// MarshalJSON projects normalized entities over auxiliary fields. Missing or
// removed normalized entities are never resurrected by the archived graph.
func (d Data) MarshalJSON() ([]byte, error) {
	type plain Data
	canonical, err := json.Marshal(plain(d))
	if err != nil {
		return nil, err
	}
	var out, extras map[string]json.RawMessage
	if err = json.Unmarshal(canonical, &out); err != nil {
		return nil, err
	}
	if len(d.Extra) > 0 {
		if err = json.Unmarshal(d.Extra, &extras); err != nil {
			return nil, err
		}
	}
	for key, value := range extras {
		if key != "legacy" && key != "folders" && key != "papers" && key != "attachments" && key != "notes" {
			out[key] = value
		}
	}
	for key, typ := range map[string]reflect.Type{"folders": reflect.TypeOf(Folder{}), "papers": reflect.TypeOf(Paper{}), "attachments": reflect.TypeOf(Attachment{}), "notes": reflect.TypeOf(Note{})} {
		var current, old []map[string]json.RawMessage
		if err = json.Unmarshal(out[key], &current); err != nil {
			return nil, err
		}
		if len(extras[key]) > 0 {
			if err = json.Unmarshal(extras[key], &old); err != nil {
				return nil, err
			}
		}
		byID := map[string]map[string]json.RawMessage{}
		for _, entity := range old {
			byID[string(entity["id"])] = entity
		}
		for i, entity := range current {
			current[i] = projectEntity(byID[string(entity["id"])], entity, typ)
		}
		if current == nil {
			current = []map[string]json.RawMessage{}
		}
		out[key], err = json.Marshal(current)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(out)
}
func projectEntity(extra, current map[string]json.RawMessage, typ reflect.Type) map[string]json.RawMessage {
	result := map[string]json.RawMessage{}
	for k, v := range extra {
		result[k] = v
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		key := strings.Split(field.Tag.Get("json"), ",")[0]
		if key == "" || key == "-" {
			continue
		}
		delete(result, key)
		value, exists := current[key]
		if !exists {
			continue
		}
		if field.Type.Kind() == reflect.Struct && field.Type.PkgPath() != "time" {
			var a, b map[string]json.RawMessage
			_ = json.Unmarshal(extra[key], &a)
			_ = json.Unmarshal(value, &b)
			value, _ = json.Marshal(projectEntity(a, b, field.Type))
		}
		result[key] = value
	}
	return result
}
func loadAuxiliary(ctx context.Context, db *gorm.DB, dataset string, data *Data) error {
	if db.Migrator().HasTable(&orm.PaperLibraryLegacyState{}) {
		var state orm.PaperLibraryLegacyState
		err := db.WithContext(ctx).Where("dataset_id=?", dataset).Take(&state).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			data.Extra = state.ExtrasJSON
		}
	}
	if db.Migrator().HasTable(&orm.PaperLibrary{}) {
		var archive orm.PaperLibrary
		err := db.WithContext(ctx).Where("dataset_id=?", dataset).Take(&archive).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			data.Legacy = archive.DataJSON
		}
	}
	return nil
}

// mergeAuxiliary keeps existing values on repeat imports, while adding new
// stable entities and source/membership IDs. Bibliography and filing remain in
// normalized tables and always win when the response is projected.
func mergeAuxiliary(oldRaw, newRaw json.RawMessage) (json.RawMessage, error) {
	var old, next map[string]any
	if len(oldRaw) > 0 {
		if err := decodeAuxiliaryJSON(oldRaw, &old); err != nil {
			return nil, err
		}
	}
	if err := decodeAuxiliaryJSON(newRaw, &next); err != nil {
		return nil, err
	}
	if old == nil {
		old = map[string]any{}
	}
	for key, value := range next {
		if key == "legacy" {
			continue
		}
		if prior, ok := old[key]; ok {
			if key == "migrationSnapshots" || key == "mergeSnapshots" || key == "restoredArchives" {
				old[key] = mergeHistoricalValues(prior, value)
			} else {
				old[key] = mergeExtraValue(prior, value)
			}
		} else {
			old[key] = value
		}
	}
	return json.Marshal(old)
}
func mergeExtraValue(old, next any) any {
	if a, ok := old.(map[string]any); ok {
		if b, ok := next.(map[string]any); ok {
			for k, v := range b {
				if prior, ok := a[k]; ok {
					// Filing changed by a user must survive a stale source
					// snapshot, including an explicit move to the root.
					if k == "collectionIds" {
						continue
					}
					a[k] = mergeExtraValue(prior, v)
				} else {
					a[k] = v
				}
			}
		}
		return a
	}
	if a, ok := old.([]any); ok {
		if b, ok := next.([]any); ok {
			keys := map[string]int{}
			key := func(v any) string {
				if m, ok := v.(map[string]any); ok {
					if id, ok := m["id"].(string); ok {
						return "id:" + id
					}
				}
				raw, _ := json.Marshal(v)
				return string(raw)
			}
			for i, v := range a {
				keys[key(v)] = i
			}
			for _, v := range b {
				k := key(v)
				if i, ok := keys[k]; ok {
					a[i] = mergeExtraValue(a[i], v)
				} else {
					keys[k] = len(a)
					a = append(a, v)
				}
			}
		}
		return a
	}
	return old
}

// MergeEntityExtras updates auxiliary facts in the caller's transaction. A nil
// value removes an obsolete fact; normalized fields remain authoritative.
func MergeEntityExtras(ctx context.Context, tx *gorm.DB, dataset, collection, id string, fields map[string]any) error {
	state := orm.PaperLibraryLegacyState{DatasetID: dataset, ExtrasJSON: json.RawMessage(`{}`), UpdatedAt: time.Now().UTC()}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=?", dataset).Take(&state).Error; err != nil {
		return err
	}
	var graph map[string]any
	if err := decodeAuxiliaryJSON(state.ExtrasJSON, &graph); err != nil {
		return err
	}
	items, _ := graph[collection].([]any)
	var entity map[string]any
	for _, item := range items {
		if m, ok := item.(map[string]any); ok && m["id"] == id {
			entity = m
			break
		}
	}
	if entity == nil {
		entity = map[string]any{"id": id}
		items = append(items, entity)
	}
	for k, v := range fields {
		if v == nil {
			delete(entity, k)
		} else {
			entity[k] = v
		}
	}
	graph[collection] = items
	raw, err := json.Marshal(graph)
	if err != nil {
		return err
	}
	return tx.Model(&state).Update("extras_json", raw).Error
}

// RemoveFolderExtras removes source memberships while retaining notes and
// annotations. The immutable archive still records the original source graph.
func RemoveFolderExtras(ctx context.Context, tx *gorm.DB, dataset string, ids []string, parent string) error {
	var state orm.PaperLibraryLegacyState
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=?", dataset).Take(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var graph map[string]any
	if err = decodeAuxiliaryJSON(state.ExtrasJSON, &graph); err != nil {
		return err
	}
	removed := map[string]bool{}
	for _, id := range ids {
		removed[id] = true
	}
	for _, key := range []string{"collections", "folders"} {
		items, _ := graph[key].([]any)
		kept := []any{}
		for _, item := range items {
			m, _ := item.(map[string]any)
			id, _ := m["id"].(string)
			if !removed[id] {
				kept = append(kept, item)
			}
		}
		graph[key] = kept
	}
	for _, key := range []string{"papers", "hiddenArxivPapers"} {
		items, _ := graph[key].([]any)
		for _, item := range items {
			m, _ := item.(map[string]any)
			if m == nil {
				continue
			}
			members, _ := m["collectionIds"].([]any)
			kept := []any{}
			for _, member := range members {
				id, _ := member.(string)
				if !removed[id] {
					kept = append(kept, member)
				}
			}
			m["collectionIds"] = kept
			if folder, _ := m["folderId"].(string); removed[folder] {
				if parent == "" {
					m["folderId"] = nil
				} else {
					m["folderId"] = parent
				}
			}
		}
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		return err
	}
	return tx.Model(&state).Updates(map[string]any{"extras_json": raw, "updated_at": time.Now().UTC()}).Error
}

// Opaque extension fields can contain 64-bit external IDs. Decode them as JSON
// numbers so a round trip never passes through lossy float64 conversion.
func decodeAuxiliaryJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func mergeHistoricalValues(old, next any) any {
	existing, ok := old.([]any)
	if !ok {
		return old
	}
	incoming, ok := next.([]any)
	if !ok {
		return old
	}
	seen := map[string]bool{}
	key := func(value any) string {
		if entity, ok := value.(map[string]any); ok {
			if id, ok := entity["id"].(string); ok {
				return "id:" + id
			}
		}
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
	for _, value := range existing {
		seen[key(value)] = true
	}
	for _, value := range incoming {
		identity := key(value)
		if !seen[identity] {
			existing = append(existing, value)
			seen[identity] = true
		}
	}
	return existing
}
