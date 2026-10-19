package documentdirectory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

type FolderRequest struct {
	InventoryID string                   `json:"inventory_id"`
	Version     int64                    `json:"inventory_version"`
	Source      json.RawMessage          `json:"source"`
	State       json.RawMessage          `json:"state"`
	Status      string                   `json:"status"`
	Nodes       []orm.DocumentFolderNode `json:"nodes"`
	Offset      int                      `json:"offset"`
	ParentKey   *string                  `json:"parent_key"`
}

func inventory(tx *gorm.DB, uid, id string) (orm.DocumentFolderInventory, error) {
	var v orm.DocumentFolderInventory
	err := tx.Where("id=? AND user_id=?", id, uid).Take(&v).Error
	return v, err
}

func folderOperation(ctx context.Context, db *gorm.DB, uid, conv string, c Command, req FolderRequest) (map[string]any, error) {
	result := map[string]any{}
	err := mutate(ctx, db, uid, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if c.Operation == "folder_begin" {
			if !currentNodeFormat(req.State) {
				return fmt.Errorf("%w: inventory_format_restart", ErrInput)
			}
			var source map[string]any
			if json.Unmarshal(req.Source, &source) != nil || source["root"] == nil {
				return ErrInput
			}
			switch source["provider"] {
			case "local", "google_drive", "feishu", "feishu_wiki":
			default:
				return ErrInput
			}
			if !json.Valid(req.State) {
				return ErrInput
			}
			v := orm.DocumentFolderInventory{ID: common.GenerateID(), UserID: uid, ConversationID: conv, SourceJSON: req.Source, StateJSON: req.State, Status: "scanning", Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&v).Error; err != nil {
				return err
			}
			result["inventory"] = v
			return nil
		}
		v, err := inventory(tx, uid, req.InventoryID)
		if err != nil {
			return err
		}
		// Inventories may resume in a later run of the owning conversation, never another chat.
		if v.ConversationID != conv {
			return ErrInput
		}
		if v.BatchID == "" && c.Operation != "folder_page" && !currentNodeFormat(v.StateJSON) {
			return fmt.Errorf("%w: inventory_format_restart", ErrInput)
		}
		switch c.Operation {
		case "folder_page":
			if req.Offset < 0 {
				return ErrInput
			}
			nodes := []orm.DocumentFolderNode{}
			q := tx.Where("inventory_id=?", v.ID)
			if req.ParentKey != nil {
				q = q.Where("parent_key=?", *req.ParentKey)
			}
			var total int64
			if err = q.Model(&orm.DocumentFolderNode{}).Count(&total).Error; err != nil {
				return err
			}
			if err = q.Order("path,id").Offset(req.Offset).Limit(100).Find(&nodes).Error; err != nil {
				return err
			}
			result["inventory"], result["nodes"], result["total"], result["next_offset"] = v, nodes, total, req.Offset+len(nodes)
			return nil
		case "folder_checkpoint":
			if v.BatchID != "" || req.Version != v.Version {
				return ErrBusy
			}
			if len(req.Nodes) > 200 || !currentNodeFormat(req.State) {
				return ErrInput
			}
			switch req.Status {
			case "scanning", "incomplete", "complete":
			default:
				return ErrInput
			}
			for _, n := range req.Nodes {
				if n.NodeKey == "" || len(n.NodeKey) > 64 || len(n.Path) > 16384 || strings.HasPrefix(n.Path, "/") {
					return ErrInput
				}
				for _, part := range strings.Split(n.Path, "/") {
					if part == ".." {
						return ErrInput
					}
				}
				switch n.Kind {
				case "directory", "file", "link", "special":
				default:
					return ErrInput
				}
				n.ID = key([]string{v.ID, n.NodeKey})
				n.InventoryID = v.ID
				n.ResourceKey = ""
				if len(n.CandidateJSON) > 0 && string(n.CandidateJSON) != "null" {
					var candidate Candidate
					if json.Unmarshal(n.CandidateJSON, &candidate) != nil || !validCandidate(candidate) {
						return ErrInput
					}
					n.ResourceKey = resourceKey(candidate)
				} else if n.Kind != "directory" {
					return ErrInput
				}
				fields := []string{"listed", "candidate_json", "resource_key"}
				if n.Kind == "directory" && n.Listed && len(n.CandidateJSON) == 0 {
					fields = []string{"listed"}
				}
				if err = tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns(fields)}).Create(&n).Error; err != nil {
					return err
				}
			}
			if req.Status == "complete" {
				var remaining int64
				if err = tx.Model(&orm.DocumentFolderNode{}).Where("inventory_id=? AND kind=? AND listed=?", v.ID, "directory", false).Count(&remaining).Error; err != nil {
					return err
				}
				var roots int64
				if err = tx.Model(&orm.DocumentFolderNode{}).Where("inventory_id=? AND path=? AND kind=?", v.ID, "", "directory").Count(&roots).Error; err != nil {
					return err
				}
				var state struct {
					Queue  []json.RawMessage `json:"queue"`
					Errors []json.RawMessage `json:"errors"`
				}
				if json.Unmarshal(req.State, &state) != nil || remaining > 0 || roots != 1 || len(state.Queue) > 0 || len(state.Errors) > 0 {
					return ErrInput
				}
			}
			var counts struct {
				Files            int64 `json:"files"`
				Directories      int64 `json:"directories"`
				EmptyDirectories int64 `json:"empty_directories"`
			}
			if err = tx.Model(&orm.DocumentFolderNode{}).Where("inventory_id=? AND resource_key<>?", v.ID, "").Count(&counts.Files).Error; err != nil {
				return err
			}
			if err = tx.Model(&orm.DocumentFolderNode{}).Where("inventory_id=? AND kind=? AND path<>?", v.ID, "directory", "").Count(&counts.Directories).Error; err != nil {
				return err
			}
			if err = tx.Model(&orm.DocumentFolderNode{}).Where("inventory_id=? AND kind=? AND path<>? AND listed=? AND resource_key='' AND NOT EXISTS (SELECT 1 FROM document_folder_nodes child WHERE child.inventory_id=document_folder_nodes.inventory_id AND child.parent_key=document_folder_nodes.node_key)", v.ID, "directory", "", true).Count(&counts.EmptyDirectories).Error; err != nil {
				return err
			}
			var state map[string]any
			if json.Unmarshal(req.State, &state) != nil || state == nil {
				return ErrInput
			}
			state["counts"] = counts
			v.Status, v.StateJSON, v.UpdatedAt, v.Version = req.Status, encoded(state), now, v.Version+1
			if err = tx.Save(&v).Error; err != nil {
				return err
			}
			if err = reconcileFolderObservations(tx, v); err != nil {
				return err
			}
			result["inventory"] = v
			return nil
		case "folder_submit":
			if v.Status == "complete" {
				if err = reconcileFolderObservations(tx, v); err != nil {
					return err
				}
			}
			if v.BatchID != "" {
				if _, err := requireDirectory(tx, uid, v.DirectoryID); err != nil {
					return err
				}
				result["directory_id"], result["batch_id"], result["status"] = v.DirectoryID, v.BatchID, "already_submitted"
				var batch orm.DocumentDirectoryBatch
				if err := tx.Where("id=? AND user_id=?", v.BatchID, uid).Take(&batch).Error; err != nil {
					return err
				}
				var progress map[string]any
				_ = json.Unmarshal(batch.ResultJSON, &progress)
				result["progress"] = progress
				result["directory_url"] = "/document-directories/" + v.DirectoryID
				return nil
			}
			if v.Status != "complete" {
				return fmt.Errorf("%w: inventory_incomplete", ErrInput)
			}
			if strings.TrimSpace(c.Name) == "" || len(c.Name) > 255 || len(c.Organization) > 4000 || len(c.Instruction) > 4000 {
				return ErrInput
			}
			var pins int64
			if err = tx.Model(&orm.DocumentDirectory{}).Where("user_id=? AND pinned=? AND deleted_at IS NULL", uid, true).Count(&pins).Error; err != nil {
				return err
			}
			if pins >= 10 {
				return ErrPinLimit
			}
			if c.Organization == "" {
				c.Organization = "按资料类型组织"
			}
			d := orm.DocumentDirectory{ID: common.GenerateID(), UserID: uid, Name: c.Name, Intent: "指定文件夹整夹收录：" + c.Name, Organization: c.Organization, Pinned: true, Version: 1, LastUsedAt: now, CreatedAt: now, UpdatedAt: now, CoverageJSON: encoded([]any{})}
			if err = tx.Create(&d).Error; err != nil {
				return err
			}
			// GORM applies the legacy true default on insert; explicitly disable auto-add.
			if err = tx.Model(&d).Update("auto_add", false).Error; err != nil {
				return err
			}
			b := orm.DocumentDirectoryBatch{ID: common.GenerateID(), UserID: uid, OwnerKey: uid, DirectoryID: d.ID, TriggerID: "folder:" + v.ID, InputJSON: encoded(Input{ConversationID: conv, FolderInventoryID: v.ID, Manual: true, Instruction: c.Instruction}), CreatedAt: now, UpdatedAt: now, ResultJSON: encoded(map[string]any{"collection_state": "pending", "classification_state": "pending", "processed": 0})}
			if err = enqueue(ctx, tx, &b, OrganizeJob, d.ID); err != nil {
				return err
			}
			if err = tx.Model(&orm.DocumentObservation{}).Where("user_id=? AND batch_id=?", uid, "inv:"+v.ID).Update("batch_id", b.ID).Error; err != nil {
				return err
			}
			v.DirectoryID, v.BatchID, v.UpdatedAt = d.ID, b.ID, now
			if err = tx.Save(&v).Error; err != nil {
				return err
			}
			result["directory_id"], result["batch_id"], result["collection_state"], result["classification_state"] = d.ID, b.ID, "pending", "pending"
			result["directory_url"] = "/document-directories/" + d.ID
			result["message"] = "已提交，等待收录。当前尚未完成收录或分类，请勿报告完成。"
			return nil
		}
		return ErrInput
	})
	return result, err
}

// Query only distinct resource keys, preserving the candidate order and payloads.
func partitionFolderCandidates(tx *gorm.DB, inventoryID string, candidates []Candidate) ([]Candidate, []Candidate, error) {
	keys, seen := []string{}, map[string]bool{}
	for _, c := range candidates {
		k := resourceKey(c)
		if !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	members := map[string]bool{}
	for start := 0; start < len(keys); start += 100 {
		var found []string
		if err := tx.Model(&orm.DocumentFolderNode{}).Where("inventory_id=? AND resource_key IN ?", inventoryID, keys[start:min(start+100, len(keys))]).Pluck("resource_key", &found).Error; err != nil {
			return nil, nil, err
		}
		for _, k := range found {
			members[k] = true
		}
	}
	inside, outside := []Candidate{}, []Candidate{}
	for _, c := range candidates {
		if members[resourceKey(c)] {
			inside = append(inside, c)
		} else {
			outside = append(outside, c)
		}
	}
	return inside, outside, nil
}

func classificationCounts(tx *gorm.DB, directoryID string) (int64, int64, error) {
	var counts []struct {
		GroupID string
		Count   int64
	}
	err := tx.Model(&orm.DocumentDirectoryEntry{}).Select("group_id, count(*) AS count").Where("directory_id=? AND excluded=?", directoryID, false).Group("group_id").Scan(&counts).Error
	var grouped, unclassified int64
	for _, row := range counts {
		if row.GroupID != "" {
			grouped += row.Count
		} else {
			unclassified = row.Count
		}
	}
	return grouped, unclassified, err
}

func reconcileFolderObservations(tx *gorm.DB, v orm.DocumentFolderInventory) error {
	var pending []orm.DocumentObservation
	if err := tx.Where("user_id=? AND conversation_id=? AND batch_id LIKE ?", v.UserID, v.ConversationID, "sc:%").Find(&pending).Error; err != nil {
		return err
	}
	for _, row := range pending {
		var o Observation
		if err := json.Unmarshal(row.PayloadJSON, &o); err != nil {
			return err
		}
		for _, id := range o.InventoryIDs {
			if id == v.ID {
				if err := saveScopedObservation(tx, row, o); err != nil {
					return err
				}
				break
			}
		}
	}
	// Reconcile pre-upgrade single-inventory observations as well.
	return reconcileLegacyFolderObservations(tx, v)
}

func reconcileLegacyFolderObservations(tx *gorm.DB, v orm.DocumentFolderInventory) error {
	var rows []orm.DocumentObservation
	marker := "inv:" + v.ID
	if err := tx.Where("user_id=? AND conversation_id=? AND (batch_id=? OR (batch_id=? AND call_id LIKE ?))", v.UserID, v.ConversationID, marker, "", "%:"+v.ID).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var observation Observation
		if err := json.Unmarshal(row.PayloadJSON, &observation); err != nil {
			return err
		}
		if len(observation.InventoryIDs) > 0 || v.Status != "complete" {
			continue
		}
		inside, outside, err := partitionFolderCandidates(tx, v.ID, observation.Candidates)
		if err != nil {
			return err
		}
		if len(outside) > 0 {
			other := row
			other.ID, other.BatchID = key([]string{row.ID, "outside"}), ""
			other.CallID = strings.TrimSuffix(row.CallID, ":"+v.ID) + ":outside"
			plain := observation
			plain.InventoryID, plain.Candidates = "", outside
			other.PayloadJSON = encoded(plain)
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&other).Error; err != nil {
				return err
			}
		}
		observation.InventoryID, observation.Candidates = v.ID, inside
		batchID := v.BatchID
		if batchID == "" {
			batchID = marker
		}
		if err := tx.Model(&row).Updates(map[string]any{"payload_json": encoded(observation), "batch_id": batchID}).Error; err != nil {
			return err
		}
	}
	return nil
}

// All writes are lease-checked short transactions. Classification never controls membership.
func collectFolder(ctx context.Context, db *gorm.DB, j asyncjob.Job, b *orm.DocumentDirectoryBatch, input Input) error {
	for {
		finished := false
		err := mutate(ctx, db, b.UserID, func(tx *gorm.DB) error {
			if err := ownsJob(tx, j); err != nil {
				return err
			}
			d, err := requireDirectory(tx, b.UserID, b.DirectoryID)
			if err != nil {
				return err
			}
			if d.Ready {
				finished = true
				var collected int64
				if err := tx.Model(&orm.DocumentDirectoryEntry{}).Where("directory_id=?", d.ID).Count(&collected).Error; err != nil {
					return err
				}
				b.ResultJSON = folderResult(b.ResultJSON, map[string]any{"collection_state": "complete", "collected": collected})
				return tx.Model(b).Update("result_json", b.ResultJSON).Error
			}
			v, err := inventory(tx, b.UserID, input.FolderInventoryID)
			if err != nil {
				return err
			}
			if v.DirectoryID != d.ID || v.Status != "complete" {
				return ErrInput
			}
			nodes := []orm.DocumentFolderNode{}
			if err = tx.Where("inventory_id=? AND resource_key<>? AND NOT EXISTS (SELECT 1 FROM document_directory_entries e WHERE e.directory_id=? AND e.resource_key=document_folder_nodes.resource_key)", v.ID, "", d.ID).Order("id").Limit(100).Find(&nodes).Error; err != nil {
				return err
			}
			for _, n := range nodes {
				var c Candidate
				if json.Unmarshal(n.CandidateJSON, &c) != nil || !validCandidate(c) {
					return ErrInput
				}
				e := entryFromCandidate(d.ID, c, time.Now().UTC())
				if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&e).Error; err != nil {
					return err
				}
			}
			var collected int64
			if err = tx.Model(&orm.DocumentDirectoryEntry{}).Where("directory_id=?", d.ID).Count(&collected).Error; err != nil {
				return err
			}
			state := "running"
			if len(nodes) == 0 {
				if _, err = makeRoom(tx, b.UserID, d.ID); err != nil {
					return err
				}
				if err = tx.Model(&d).Updates(map[string]any{"ready": true, "updated_at": time.Now().UTC()}).Error; err != nil {
					return err
				}
				finished = true
				state = "complete"
			}
			b.Status = "running"
			b.ResultJSON = folderResult(b.ResultJSON, map[string]any{"collection_state": state, "collected": collected, "classification_state": "pending"})
			return tx.Model(b).Updates(map[string]any{"status": b.Status, "result_json": b.ResultJSON}).Error
		})
		if err != nil {
			return err
		}
		if finished {
			return nil
		}
	}
}

func organizeFolder(ctx context.Context, db *gorm.DB, j asyncjob.Job, b *orm.DocumentDirectoryBatch, input Input, generate func(string, any) (Proposal, error)) error {
	if err := collectFolder(ctx, db, j, b, input); err != nil {
		return err
	}
	for {
		var result struct {
			Processed int `json:"processed"`
		}
		if err := json.Unmarshal(b.ResultJSON, &result); err != nil {
			return err
		}
		var d orm.DocumentDirectory
		var groups []orm.DocumentDirectoryGroup
		var entries []orm.DocumentDirectoryEntry
		err := mutate(ctx, db, b.UserID, func(tx *gorm.DB) error {
			if err := ownsJob(tx, j); err != nil {
				return err
			}
			var err error
			d, err = requireDirectory(tx, b.UserID, b.DirectoryID)
			if err != nil {
				return err
			}
			if err = tx.Where("directory_id=?", d.ID).Order("position,id").Find(&groups).Error; err != nil {
				return err
			}
			if err = tx.Where("directory_id=?", d.ID).Order("id").Offset(result.Processed).Limit(30).Find(&entries).Error; err != nil {
				return err
			}
			b.ResultJSON = folderResult(b.ResultJSON, map[string]any{"classification_state": "running"})
			return tx.Model(b).Update("result_json", b.ResultJSON).Error
		})
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return mutate(ctx, db, b.UserID, func(tx *gorm.DB) error {
				if err := ownsJob(tx, j); err != nil {
					return err
				}
				grouped, unclassified, err := classificationCounts(tx, d.ID)
				if err != nil {
					return err
				}
				b.Status = "succeeded"
				b.ResultJSON = folderResult(b.ResultJSON, map[string]any{"classification_state": "complete", "grouped": grouped, "unclassified": unclassified})
				return tx.Model(b).Updates(map[string]any{"status": b.Status, "result_json": b.ResultJSON, "updated_at": time.Now().UTC()}).Error
			})
		}
		chunk := Input{Manual: true, Entries: entries, FolderBatch: true, FolderOffset: result.Processed + len(entries)}
		// Only the evidence for this inventory is eligible for enrichment.
		var observations []orm.DocumentObservation
		v, loadErr := inventory(db, b.UserID, input.FolderInventoryID)
		if loadErr != nil {
			return loadErr
		}
		if err = db.Where("user_id=? AND batch_id IN ?", b.UserID, []string{b.ID, v.BatchID}).Find(&observations).Error; err != nil {
			return err
		}
		selected := map[string]bool{}
		for _, e := range entries {
			selected[e.ResourceKey] = true
		}
		for _, o := range observations {
			var obs Observation
			if json.Unmarshal(o.PayloadJSON, &obs) != nil {
				continue
			}
			filtered := []Candidate{}
			for _, c := range obs.Candidates {
				if selected[resourceKey(c)] {
					filtered = append(filtered, c)
				}
			}
			obs.Candidates = filtered
			o.PayloadJSON = encoded(obs)
			chunk.Observations = append(chunk.Observations, o)
		}
		resources := organizerResources(chunk, entries)
		p := Proposal{Groups: []orm.DocumentDirectoryGroup{}, Assignments: []Assignment{}}
		if len(resources) > 0 {
			p, err = generate("organize", map[string]any{"directory": d, "groups": groups, "entries": entries, "resources": resources, "instruction": input.Instruction + "\nAll supplied resources already belong to the selected folder. Classify only; reuse existing groups. Keep reasons brief. Uncertain items remain unclassified."})
			if err != nil {
				return err
			}
		}
		if err = applyOrganization(ctx, db, j, b, d.Version, chunk, p); err != nil {
			return err
		}
	}
}

func folderResult(previous json.RawMessage, next map[string]any) json.RawMessage {
	var result map[string]any
	_ = json.Unmarshal(previous, &result)
	if result == nil {
		result = map[string]any{}
	}
	for k, v := range next {
		result[k] = v
	}
	return encoded(result)
}

func FolderNodes(ctx context.Context, db *gorm.DB, uid, dir, parent string, offset int) (map[string]any, error) {
	if offset < 0 {
		return nil, ErrInput
	}
	if _, err := requireDirectory(db.WithContext(ctx), uid, dir); err != nil {
		return nil, err
	}
	var v orm.DocumentFolderInventory
	if err := db.WithContext(ctx).Where("user_id=? AND directory_id=?", uid, dir).Take(&v).Error; err != nil {
		return nil, err
	}
	q := db.WithContext(ctx).Where("inventory_id=? AND parent_key=?", v.ID, parent)
	nodes := []orm.DocumentFolderNode{}
	var total int64
	if err := q.Model(&orm.DocumentFolderNode{}).Count(&total).Error; err != nil {
		return nil, err
	}
	if err := q.Order("path,id").Offset(offset).Limit(100).Find(&nodes).Error; err != nil {
		return nil, err
	}
	return map[string]any{"nodes": nodes, "total": total, "next_offset": offset + len(nodes)}, nil
}

type EntryPageQuery struct {
	Offset, Limit int
	Group         *string
	Query, State  string
}

func entryPage(db *gorm.DB, dir string, page EntryPageQuery, detail *Detail) error {
	q := db.Model(&orm.DocumentDirectoryEntry{}).Where("directory_id=? AND excluded=?", dir, page.State == "excluded")
	type count struct {
		GroupID string
		Count   int64
	}
	var counts []count
	if err := q.Select("group_id, count(*) as count").Group("group_id").Scan(&counts).Error; err != nil {
		return err
	}
	detail.GroupCounts = map[string]int64{}
	for _, c := range counts {
		detail.GroupCounts[c.GroupID] = c.Count
	}
	q = db.Model(&orm.DocumentDirectoryEntry{}).Where("directory_id=? AND excluded=?", dir, page.State == "excluded")
	if page.Group != nil {
		q = q.Where("group_id=?", *page.Group)
	}
	if query := strings.TrimSpace(page.Query); query != "" {
		value := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(query)) + "%"
		q = q.Where("(LOWER(title) LIKE ? ESCAPE '!' OR LOWER(resource_id) LIKE ? ESCAPE '!')", value, value)
	}
	switch page.State {
	case "unconfirmed":
		q = q.Where("confirmed=?", false)
	case "unread":
		q = q.Where("read_state=?", "unread")
	case "inaccessible":
		q = q.Where("access_state IN ?", []string{"denied", "deleted", "unavailable"})
	}
	if err := q.Count(&detail.Total).Error; err != nil {
		return err
	}
	return q.Order("created_at,id").Offset(page.Offset).Limit(page.Limit).Find(&detail.Entries).Error
}
