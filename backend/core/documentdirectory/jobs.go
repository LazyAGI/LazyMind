package documentdirectory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"
)

var LoadChatModel func(context.Context, *gorm.DB, string, string) (map[string]any, error)

type Target struct {
	DirectoryID  string   `json:"directory_id"`
	Name         string   `json:"name"`
	Intent       string   `json:"intent"`
	Organization string   `json:"organization"`
	ResourceKeys []string `json:"resource_keys"`
}
type Assignment struct {
	ResourceKey   string `json:"resource_key"`
	GroupID       string `json:"group_id"`
	Confirmed     bool   `json:"confirmed"`
	Reason        string `json:"reason"`
	EvidenceBasis string `json:"evidence_basis,omitempty"`
}
type Proposal struct {
	Usage       map[string]any               `json:"-"`
	Targets     []Target                     `json:"targets"`
	Groups      []orm.DocumentDirectoryGroup `json:"groups"`
	Assignments []Assignment                 `json:"assignments"`
}
type modelRequest struct {
	Mode      string         `json:"mode"`
	Input     any            `json:"input"`
	LLMConfig map[string]any `json:"llm_config"`
}
type modelResponse struct {
	Usage     map[string]any `json:"usage"`
	Status    string         `json:"status"`
	Output    Proposal       `json:"output"`
	ErrorCode string         `json:"error_code"`
}

func callModel(ctx context.Context, db *gorm.DB, uid, conversationID, mode string, input any) (Proposal, error) {
	if LoadChatModel == nil {
		return Proposal{}, errors.New("directory model is unavailable")
	}
	config, err := LoadChatModel(ctx, db, uid, conversationID)
	if err != nil {
		return Proposal{}, err
	}
	started := time.Now()
	var result modelResponse
	err = common.ApiPost(ctx, common.JoinURL(common.ChatServiceEndpoint(), "/api/directory/plan"), modelRequest{Mode: mode, Input: input, LLMConfig: config}, nil, &result, 5*time.Minute)
	if result.Usage == nil {
		result.Usage = map[string]any{}
	}
	result.Usage["elapsed_ms"] = time.Since(started).Milliseconds()
	result.Output.Usage = result.Usage
	if err != nil {
		result.Output.Usage["incomplete"] = true
		return result.Output, err
	}
	if result.Status != "succeeded" {
		return result.Output, errors.New("directory model failed: " + result.ErrorCode)
	}
	if mode == "route" && result.Output.Targets == nil || mode == "organize" && result.Output.Assignments == nil {
		return result.Output, ErrInput
	}
	return result.Output, nil
}

func RegisterJobs() {
	asyncjob.Register(PlanJob, handleJob)
	asyncjob.Register(OrganizeJob, handleJob)
}

func enqueue(ctx context.Context, tx *gorm.DB, b *orm.DocumentDirectoryBatch, jobType, resource string) error {
	idempotencyKey := b.ID
	if b.JobID != "" {
		// Manual retries must never resurrect the identity of a canceled worker.
		idempotencyKey = key([]string{b.ID, b.JobID})
	}
	j, err := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: jobType, ResourceType: jobType, ResourceID: resource, IdempotencyKey: idempotencyKey, Payload: map[string]string{"batch_id": b.ID}, MaxAttempts: 3, CreateUserID: b.UserID})
	if err != nil {
		return err
	}
	b.JobID = j.ID
	b.Status = "pending"
	b.Acknowledged = false
	if b.ParentID != "" {
		if err := tx.Model(&orm.DocumentDirectoryBatch{}).Where("id=? AND user_id=?", b.ParentID, b.UserID).Update("acknowledged", false).Error; err != nil {
			return err
		}
	}
	b.UpdatedAt = time.Now().UTC()
	return tx.Save(b).Error
}

// QueueIdle runs after the existing idle-event validity check, before memory
// history is inspected. Observation ownership is durable and independent of Redis.
func QueueIdle(ctx context.Context, db *gorm.DB, event orm.ConversationIdleEvent) error {
	err := mutate(ctx, db, event.UserID, func(tx *gorm.DB) error {
		var valid int64
		if err := tx.Model(&orm.ConversationIdleEvent{}).Where("event_id=? AND status=? AND due_at<=?", event.EventID, "waiting", time.Now().UTC()).Count(&valid).Error; err != nil {
			return err
		}
		if valid == 0 {
			return nil
		}
		var observations []orm.DocumentObservation
		if err := tx.Where("user_id=? AND conversation_id=? AND batch_id=? AND created_at<=?", event.UserID, event.ConversationID, "", event.LastActivityAt).Order("created_at,id").Find(&observations).Error; err != nil {
			return err
		}
		if len(observations) == 0 {
			return nil
		}
		now := time.Now().UTC()
		b := orm.DocumentDirectoryBatch{ID: common.GenerateID(), UserID: event.UserID, OwnerKey: event.UserID, TriggerID: key(event.EventID), InputJSON: encoded(Input{Observations: observations}), Status: "pending", CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&b).Error; err != nil {
			return err
		}
		ids := make([]string, 0, len(observations))
		for _, o := range observations {
			ids = append(ids, o.ID)
		}
		if err := tx.Model(&orm.DocumentObservation{}).Where("id IN ? AND batch_id=?", ids, "").Update("batch_id", b.ID).Error; err != nil {
			return err
		}
		return enqueue(ctx, tx, &b, PlanJob, event.UserID)
	})
	if errors.Is(err, ErrDisabled) {
		return nil
	}
	return err
}

func enqueueManual(ctx context.Context, tx *gorm.DB, uid string, d orm.DocumentDirectory, c Command, result map[string]any) error {
	var active int64
	if err := tx.Model(&orm.AsyncJob{}).Where("create_user_id=? AND job_type=? AND resource_id=? AND status IN ?", uid, OrganizeJob, d.ID, []string{"pending", "running"}).Count(&active).Error; err != nil {
		return err
	}
	if active > 0 {
		return ErrBusy
	}
	var entries []orm.DocumentDirectoryEntry
	if err := tx.Where("directory_id=? AND excluded=?", d.ID, false).Find(&entries).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	if c.Operation == "reorganize" {
		if c.Organization == "" {
			return ErrInput
		}
		d.ID = common.GenerateID()
		d.Ready = false
		d.Pinned = false
		d.Organization = c.Organization
		d.Version = 1
		d.CreatedAt = now
		d.UpdatedAt = now
		d.LastUsedAt = now
		d.UseUntil = nil
		d.OrganizedAt = nil
		if c.Name != "" {
			d.Name = c.Name
		}
		if c.Intent != "" {
			d.Intent = c.Intent
		}
		if err := tx.Create(&d).Error; err != nil {
			return err
		}
		for i := range entries {
			entries[i].Pinned = false
			entries[i].GroupID = ""
		}
	} else if c.Organization != "" && c.Organization != d.Organization {
		return ErrInput
	}
	b := orm.DocumentDirectoryBatch{ID: common.GenerateID(), UserID: uid, OwnerKey: uid, DirectoryID: d.ID, TriggerID: common.GenerateID(), InputJSON: encoded(Input{ConversationID: c.ConversationID, Entries: entries, Instruction: c.Instruction, Manual: true, Reorganize: c.Operation == "reorganize", SourceDirectoryID: c.DirectoryID}), Status: "pending", CreatedAt: now, UpdatedAt: now}
	var folder orm.DocumentFolderInventory
	folderErr := tx.Where("directory_id=? AND user_id=?", c.DirectoryID, uid).Take(&folder).Error
	if folderErr != nil && !errors.Is(folderErr, gorm.ErrRecordNotFound) {
		return folderErr
	}
	if folderErr == nil {
		if c.Operation == "reorganize" {
			var pins int64
			if err := tx.Model(&orm.DocumentDirectory{}).Where("user_id=? AND pinned=? AND deleted_at IS NULL", uid, true).Count(&pins).Error; err != nil {
				return err
			}
			if pins >= 10 {
				return ErrPinLimit
			}
			if err := tx.Model(&d).Updates(map[string]any{"pinned": true, "auto_add": false}).Error; err != nil {
				return err
			}
			original := folder.ID
			folder.ID = common.GenerateID()
			folder.DirectoryID = d.ID
			folder.BatchID = b.ID
			var state map[string]any
			if err := json.Unmarshal(folder.StateJSON, &state); err != nil {
				return err
			}
			if state == nil {
				state = map[string]any{}
			}
			state["reorganization"] = true
			folder.StateJSON = encoded(state)
			folder.CreatedAt = now
			folder.UpdatedAt = now
			if err := tx.Create(&folder).Error; err != nil {
				return err
			}
			var nodes []orm.DocumentFolderNode
			if err := tx.Where("inventory_id=?", original).Find(&nodes).Error; err != nil {
				return err
			}
			for i := range nodes {
				nodes[i].InventoryID = folder.ID
				nodes[i].ID = key([]string{folder.ID, nodes[i].NodeKey})
			}
			if len(nodes) > 0 {
				if err := tx.CreateInBatches(nodes, 100).Error; err != nil {
					return err
				}
			}
			for i := range entries {
				copyEntryIdentity(&entries[i], d.ID)
				entries[i].Pinned = false
				entries[i].Confirmed = false
			}
			if len(entries) > 0 {
				if err := tx.CreateInBatches(entries, 100).Error; err != nil {
					return err
				}
			}
			// Preserve exclusions from the original view while retaining the source snapshot.
			var excluded []orm.DocumentDirectoryEntry
			if err := tx.Where("directory_id=? AND excluded=?", c.DirectoryID, true).Find(&excluded).Error; err != nil {
				return err
			}
			for i := range excluded {
				copyEntryIdentity(&excluded[i], d.ID)
			}
			if len(excluded) > 0 {
				if err := tx.CreateInBatches(excluded, 100).Error; err != nil {
					return err
				}
			}
		}
		b.InputJSON = encoded(Input{ConversationID: c.ConversationID, FolderInventoryID: folder.ID, Manual: true, Instruction: c.Instruction, Reorganize: c.Operation == "reorganize", SourceDirectoryID: c.DirectoryID})
		b.ResultJSON = encoded(map[string]any{"collection_state": "pending", "classification_state": "pending", "processed": 0})
	}

	if c.Operation == "reorganize" && errors.Is(folderErr, gorm.ErrRecordNotFound) {
		for i := range entries {
			copyEntryIdentity(&entries[i], d.ID)
			entries[i].Confirmed = false
		}
		if len(entries) > 0 {
			if err := tx.CreateInBatches(entries, 100).Error; err != nil {
				return err
			}
		}
	}

	if err := enqueue(ctx, tx, &b, OrganizeJob, d.ID); err != nil {
		return err
	}
	result["batch"] = b
	return tx.Model(&orm.DocumentDirectory{}).Where("id=?", c.DirectoryID).Update("last_used_at", now).Error
}

func batchAction(ctx context.Context, tx *gorm.DB, uid string, c Command, result map[string]any) error {
	var b orm.DocumentDirectoryBatch
	if err := tx.Where("id=? AND user_id=?", c.BatchID, uid).Take(&b).Error; err != nil {
		return err
	}
	var j orm.AsyncJob
	if err := tx.Where("id=? AND create_user_id=?", b.JobID, uid).Take(&j).Error; err != nil {
		return err
	}
	if c.Operation == "cancel" {
		if b.Status == "succeeded" {
			return ErrInput
		}
		if err := tx.Model(&orm.AsyncJob{}).Where("id=? AND status IN ?", j.ID, []string{"pending", "running"}).Updates(map[string]any{"status": "canceled", "lock_until": nil, "locked_by": "", "finished_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		b.Status = "canceled"
		b.UpdatedAt = time.Now().UTC()
		return tx.Save(&b).Error
	}
	if j.Status != "failed" && j.Status != "canceled" {
		return ErrInput
	}
	if b.Status == "succeeded" {
		return ErrInput
	}
	if b.DirectoryID != "" {
		if _, err := requireDirectory(tx, uid, b.DirectoryID); err != nil {
			return err
		}
	}
	err := enqueue(ctx, tx, &b, j.JobType, j.ResourceID)
	result["batch"] = b
	return err
}

func requireDirectory(tx *gorm.DB, uid, id string) (orm.DocumentDirectory, error) {
	var d orm.DocumentDirectory
	err := tx.Where("id=? AND user_id=? AND deleted_at IS NULL", id, uid).Take(&d).Error
	return d, err
}

func ownsJob(tx *gorm.DB, j asyncjob.Job) error {
	// Hold the execution row until the short commit finishes. A lease reaper
	// must not reclaim this attempt between validation and writing its result.
	var execution orm.AsyncJob
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id=? AND create_user_id=? AND status=? AND attempt_count=? AND lock_until>?", j.ID, j.CreateUserID, asyncjob.StatusRunning, j.AttemptCount, time.Now().UTC()).Take(&execution).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return maintenanceLeaseLost
	}
	if err != nil {
		return err
	}
	var count int64
	if err = tx.Model(&orm.DocumentDirectoryBatch{}).Where("job_id=? AND user_id=? AND status<>?", j.ID, j.CreateUserID, "canceled").Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return maintenanceLeaseLost
	}
	return nil
}

var maintenanceLeaseLost = errors.New("document directory execution lease lost")

func handleJob(ctx context.Context, j asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	db := store.DB()
	var payload struct {
		BatchID string `json:"batch_id"`
	}
	if json.Unmarshal(j.PayloadJSON, &payload) != nil || payload.BatchID == "" {
		return asyncjob.Result{Permanent: true, ErrorCode: "invalid_payload"}, ErrInput
	}
	var b orm.DocumentDirectoryBatch
	if err := db.WithContext(ctx).Where("id=? AND user_id=?", payload.BatchID, j.CreateUserID).Take(&b).Error; err != nil {
		return asyncjob.Result{Permanent: true, ErrorCode: "batch_missing"}, err
	}
	if b.Status == "succeeded" || b.Status == "canceled" {
		return asyncjob.Result{ResultJSON: b.ResultJSON}, nil
	}
	var input Input
	if err := json.Unmarshal(b.InputJSON, &input); err != nil {
		return asyncjob.Result{Permanent: true, ErrorCode: "invalid_input"}, err
	}
	// Older idle batches already retain their source conversation in observations.
	if input.ConversationID == "" && len(input.Observations) > 0 {
		input.ConversationID = input.Observations[0].ConversationID
	}
	var err error
	generate := func(mode string, data any) (Proposal, error) {
		if err := mutate(ctx, db, b.UserID, func(tx *gorm.DB) error { return ownsJob(tx, j) }); err != nil {
			return Proposal{}, err
		}
		p, modelErr := callModel(ctx, db, b.UserID, input.ConversationID, mode, data)
		saveErr := mutate(ctx, db, b.UserID, func(tx *gorm.DB) error {
			if e := ownsJob(tx, j); e != nil {
				return e
			}
			var result map[string]any
			_ = json.Unmarshal(b.ResultJSON, &result)
			if result == nil {
				result = map[string]any{}
			}
			attempts, _ := result["model_attempts"].([]any)
			attempts = append(attempts, map[string]any{"job_id": j.ID, "attempt": j.AttemptCount, "mode": mode, "succeeded": modelErr == nil, "usage": p.Usage})
			result["model_attempts"] = attempts
			result["usage"] = modelUsage(attempts)
			b.ResultJSON = encoded(result)
			return tx.Model(&b).Update("result_json", b.ResultJSON).Error
		})
		if saveErr != nil {
			return p, saveErr
		}
		return p, modelErr
	}
	if input.FolderInventoryID != "" {
		err = organizeFolder(ctx, db, j, &b, input, generate)
	} else if j.JobType == PlanJob {
		err = routeChunks(ctx, db, j, &b, input, generate)
	} else {
		err = organizeChunks(ctx, db, j, &b, input, generate)
	}
	if err != nil {
		return asyncjob.Result{ErrorCode: "directory_organize_failed", Permanent: errors.Is(err, ErrInput) || errors.Is(err, ErrDisabled)}, err
	}
	return asyncjob.Result{ResultJSON: b.ResultJSON}, nil
}

// Project model context without changing the durable snapshot used for retries.
func routeInput(input Input, directories []orm.DocumentDirectory) map[string]any {
	type resource struct {
		Candidate
		Evidence []map[string]any `json:"evidence,omitempty"`
	}
	resources := map[string]*resource{}
	for k, c := range candidates(input) {
		c.Excerpt = "" // Bodies live only in evidence, including older distinct windows.
		resources[k] = &resource{Candidate: c}
	}
	observations := make([]map[string]any, 0, len(input.Observations))
	evidenceIndex := map[string]int{}
	for _, row := range input.Observations {
		var o Observation
		if json.Unmarshal(row.PayloadJSON, &o) != nil {
			continue
		}
		keys, seen := []string{}, map[string]bool{}
		for _, c := range o.Candidates {
			k := resourceKey(c)
			if !seen[k] {
				keys = append(keys, k)
				seen[k] = true
			}
			if c.Excerpt == "" {
				continue
			}
			r := resources[k]
			evidenceKey := key([]any{k, c.Excerpt, c.Locator, c.ReadState, c.ReadAt, c.ContentVersion})
			index, found := evidenceIndex[evidenceKey]
			if !found {
				index = len(r.Evidence)
				evidenceIndex[evidenceKey] = index
				r.Evidence = append(r.Evidence, map[string]any{
					"excerpt": c.Excerpt, "locator": c.Locator, "read_state": c.ReadState,
					"read_at": c.ReadAt, "content_version": c.ContentVersion, "observation_ids": []string{},
				})
			}
			ids := r.Evidence[index]["observation_ids"].([]string)
			if len(ids) == 0 || ids[len(ids)-1] != row.ID {
				r.Evidence[index]["observation_ids"] = append(ids, row.ID)
			}
		}
		observations = append(observations, map[string]any{
			"id": row.ID, "query": row.Query, "source": o.Source, "operation": o.Operation, "scope": o.Scope,
			"status": o.Status, "incomplete": o.Incomplete, "resource_keys": keys,
		})
	}
	catalog := make([]map[string]any, 0, len(directories))
	for _, d := range directories {
		catalog = append(catalog, map[string]any{"id": d.ID, "name": d.Name, "intent": d.Intent, "organization": d.Organization, "auto_add": d.AutoAdd, "ready": d.Ready})
	}
	return map[string]any{"input": map[string]any{"observations": observations, "instruction": input.Instruction},
		"directories": catalog, "resources": resources}
}

func copyEntryIdentity(e *orm.DocumentDirectoryEntry, directoryID string) {
	e.ID, e.DirectoryID, e.GroupID = common.GenerateID(), directoryID, ""
}

func candidates(input Input) map[string]Candidate {
	items := map[string]Candidate{}
	for _, o := range input.Observations {
		var p Observation
		if json.Unmarshal(o.PayloadJSON, &p) != nil {
			continue
		}
		for _, c := range p.Candidates {
			k := resourceKey(c)
			items[k] = mergeCandidate(items[k], c)
		}
	}
	for _, e := range input.Entries {
		if e.Excluded {
			continue
		}
		c := Candidate{Source: e.Source, Namespace: e.Namespace, ResourceID: e.ResourceID, Title: e.Title, Locator: e.LocatorJSON, ReadState: e.ReadState, AccessState: e.AccessState, ContentVersion: e.ContentVersion, ReadAt: e.ReadAt}
		k := resourceKey(c)
		if observed, ok := items[k]; ok {
			items[k] = mergeCandidate(c, observed)
		} else {
			items[k] = c
		}
	}
	return items
}

func organizerResources(input Input, entries []orm.DocumentDirectoryEntry) map[string]Candidate {
	resources := candidates(input)
	for _, e := range entries {
		if e.Excluded {
			delete(resources, e.ResourceKey)
		}
	}
	return resources
}

// Merge metadata without combining excerpts or windows from different reads.
func mergeCandidate(old, next Candidate) Candidate {
	if old.ResourceID == "" {
		return next
	}
	merged := old
	if next.Title != "" && next.Title != next.ResourceID && (next.ReadAt == nil || old.ReadAt == nil || !next.ReadAt.Before(*old.ReadAt)) {
		merged.Title = next.Title
	}
	var locator, incoming map[string]any
	_ = json.Unmarshal(old.Locator, &locator)
	_ = json.Unmarshal(next.Locator, &incoming)
	if locator == nil {
		locator = map[string]any{}
	}
	newRead := next.ReadAt != nil && (old.ReadAt == nil || !next.ReadAt.Before(*old.ReadAt))
	windowFields := map[string]bool{"offset": true, "end_line": true, "eof": true, "next_offset": true, "line_unit": true, "truncated_lines": true, "range_kind": true, "start_message_id": true, "end_message_id": true}
	if newRead {
		for field := range windowFields {
			delete(locator, field)
		}
		merged.ReadAt, merged.ReadState, merged.Excerpt = next.ReadAt, next.ReadState, next.Excerpt
	} else if old.ReadAt == nil && next.Excerpt != "" {
		merged.Excerpt = next.Excerpt
	}
	for field, value := range incoming {
		text, isText := value.(string)
		if value == nil || (isText && text == "") || (windowFields[field] && !newRead && old.ReadAt != nil) {
			continue
		}
		locator[field] = value
	}
	merged.Locator = encoded(locator)
	if next.AccessState != "" && next.AccessState != "unknown" && (next.ReadAt == nil || newRead) {
		merged.AccessState = next.AccessState
	}
	if next.ContentVersion != "" && (next.ReadAt == nil || newRead) {
		merged.ContentVersion = next.ContentVersion
	}
	return merged
}

func applyTargets(ctx context.Context, db *gorm.DB, j asyncjob.Job, b *orm.DocumentDirectoryBatch, input Input, p Proposal) error {
	return mutate(ctx, db, b.UserID, func(tx *gorm.DB) error {
		if err := ownsJob(tx, j); err != nil {
			return err
		}
		resources := candidates(input)
		now := time.Now().UTC()
		seen := map[string]bool{}
		if len(p.Targets) > 20 {
			return ErrInput
		}
		for _, t := range p.Targets {
			if len(t.ResourceKeys) == 0 {
				continue
			}
			selected := map[string]bool{}
			for _, k := range t.ResourceKeys {
				if _, ok := resources[k]; !ok {
					return ErrInput
				}
				selected[k] = true
			}
			var d orm.DocumentDirectory
			if t.DirectoryID != "" {
				var err error
				d, err = requireDirectory(tx, b.UserID, t.DirectoryID)
				if err != nil {
					return err
				}
				if !d.AutoAdd {
					continue
				}
			} else {
				latest := map[string]time.Time{}
				for _, o := range input.Observations {
					var obs Observation
					if json.Unmarshal(o.PayloadJSON, &obs) == nil {
						for _, c := range obs.Candidates {
							k := resourceKey(c)
							if o.CreatedAt.After(latest[k]) {
								latest[k] = o.CreatedAt
							}
						}
					}
				}
				for k := range selected {
					var n int64
					if err := tx.Model(&orm.DocumentDirectoryEntry{}).Joins("JOIN document_directories d ON d.id=document_directory_entries.directory_id").Where("d.user_id=? AND d.deleted_at IS NOT NULL AND d.deleted_at>=? AND document_directory_entries.resource_key=?", b.UserID, latest[k], k).Count(&n).Error; err != nil {
						return err
					}
					if n > 0 {
						delete(selected, k)
					}
				}
				if len(selected) == 0 {
					continue
				}
				if t.Name == "" || t.Intent == "" || len(t.Name) > 255 || len(t.Intent) > 4000 {
					return ErrInput
				}
				if t.Organization == "" {
					t.Organization = "按资料类型组织"
				}
				// Exact repeated intent proposals reuse their reservation. Semantic
				// ambiguity is resolved by the model against the serialized catalog.
				err := tx.Where("user_id=? AND intent=? AND organization=? AND deleted_at IS NULL", b.UserID, t.Intent, t.Organization).Take(&d).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					d = orm.DocumentDirectory{ID: common.GenerateID(), UserID: b.UserID, Name: t.Name, Intent: t.Intent, Organization: t.Organization, AutoAdd: true, Version: 1, LastUsedAt: now, CreatedAt: now, UpdatedAt: now, CoverageJSON: encoded([]any{})}
					if err = tx.Create(&d).Error; err != nil {
						return err
					}
				} else if err != nil {
					return err
				}
			}
			if !d.AutoAdd {
				continue
			}
			if seen[d.ID] {
				return ErrInput
			}
			seen[d.ID] = true
			childInput := Input{ConversationID: input.ConversationID, Observations: []orm.DocumentObservation{}}
			for _, o := range input.Observations {
				var obs Observation
				if err := json.Unmarshal(o.PayloadJSON, &obs); err != nil {
					return err
				}
				filtered := []Candidate{}
				for _, c := range obs.Candidates {
					if selected[resourceKey(c)] {
						filtered = append(filtered, c)
					}
				}
				obs.Candidates = filtered
				o.PayloadJSON = encoded(obs)
				childInput.Observations = append(childInput.Observations, o)
			}
			child := orm.DocumentDirectoryBatch{ID: common.GenerateID(), UserID: b.UserID, OwnerKey: b.UserID, ParentID: b.ID, DirectoryID: d.ID, TriggerID: b.ID + ":" + d.ID, InputJSON: encoded(childInput), Status: "pending", CreatedAt: now, UpdatedAt: now}
			if err := enqueue(ctx, tx, &child, OrganizeJob, d.ID); err != nil {
				return err
			}
		}
		b.Status = "succeeded"
		b.UpdatedAt = now
		b.Acknowledged = len(seen) == 0
		b.ResultJSON = batchResult(b.ResultJSON, map[string]any{"targets": len(seen), "usage": p.Usage})
		return tx.Save(b).Error
	})
}

func applyOrganization(ctx context.Context, db *gorm.DB, j asyncjob.Job, b *orm.DocumentDirectoryBatch, version int64, input Input, p Proposal) error {
	return mutate(ctx, db, b.UserID, func(tx *gorm.DB) error {
		if err := ownsJob(tx, j); err != nil {
			return err
		}
		d, err := requireDirectory(tx, b.UserID, b.DirectoryID)
		if err != nil {
			return err
		}
		if !input.Manual && !d.AutoAdd {
			return ErrInput
		}
		var already orm.DocumentDirectoryBatch
		if err := tx.Where("id=?", b.ID).Take(&already).Error; err != nil {
			return err
		}
		if already.Status == "succeeded" {
			b.ResultJSON = already.ResultJSON
			return nil
		}
		if d.Version != version {
			return ErrBusy
		}
		if already.Status == "canceled" {
			return maintenanceLeaseLost
		}
		now := time.Now().UTC()
		resources := candidates(input)
		groups := map[string]string{"": ""}
		var current []orm.DocumentDirectoryGroup
		if err := tx.Where("directory_id=?", d.ID).Find(&current).Error; err != nil {
			return err
		}
		for _, g := range current {
			groups[g.ID] = g.ID
		}
		// Allocate the whole proposed tree before validating parents.
		for _, g := range p.Groups {
			if g.ID == "" || g.Name == "" || len(g.Name) > 255 {
				return ErrInput
			}
			if _, ok := groups[g.ID]; ok {
				return ErrInput
			}
			groups[g.ID] = common.GenerateID()
		}
		used := map[string]bool{}
		for _, a := range p.Assignments {
			if a.EvidenceBasis != "" && a.EvidenceBasis != "read" && a.EvidenceBasis != "metadata" {
				return ErrInput
			}
			if used[a.ResourceKey] {
				return ErrInput
			}
			used[a.ResourceKey] = true
			if _, ok := resources[a.ResourceKey]; !ok {
				return ErrInput
			}
			if _, ok := groups[a.GroupID]; !ok {
				return ErrInput
			}
		}
		if len(p.Assignments) == 0 && !d.Ready && !input.Chunked && !input.Reorganize {
			b.Status = "succeeded"
			b.Acknowledged = true
			b.ResultJSON = batchResult(b.ResultJSON, map[string]any{"added": 0, "usage": p.Usage})
			if err := tx.Save(b).Error; err != nil {
				return err
			}
			if err := tx.Delete(&d).Error; err != nil {
				return err
			}
			return acknowledgeParents(tx, b.UserID, b.ParentID)
		}
		// Only create groups actually used, including their ancestors.
		proposed := map[string]orm.DocumentDirectoryGroup{}
		for _, g := range p.Groups {
			proposed[g.ID] = g
		}
		needed := map[string]bool{}
		for _, a := range p.Assignments {
			var fixed int64
			fixedQuery := tx.Model(&orm.DocumentDirectoryEntry{}).Where("directory_id=? AND resource_key=?", d.ID, a.ResourceKey)
			if readAt := resources[a.ResourceKey].ReadAt; readAt != nil {
				fixedQuery = fixedQuery.Where("pinned=? OR excluded=? OR read_at>?", true, true, readAt.Truncate(time.Microsecond))
			} else {
				fixedQuery = fixedQuery.Where("pinned=? OR excluded=?", true, true)
			}
			if err := fixedQuery.Count(&fixed).Error; err != nil {
				return err
			}
			if fixed > 0 {
				continue
			}
			path := map[string]bool{}
			id := a.GroupID
			for id != "" {
				if path[id] {
					return ErrInput
				}
				path[id] = true
				g, ok := proposed[id]
				if !ok {
					break
				}
				needed[id] = true
				if _, ok := groups[g.ParentID]; !ok {
					return ErrInput
				}
				id = g.ParentID
			}
		}
		for _, g := range p.Groups {
			if !needed[g.ID] {
				continue
			}
			g.ID = groups[g.ID]
			g.ParentID = groups[g.ParentID]
			g.DirectoryID = d.ID
			if err := tx.Create(&g).Error; err != nil {
				return err
			}
		}
		added, pending, excluded := 0, 0, 0
		var excludedEntries []orm.DocumentDirectoryEntry
		if err := tx.Where("directory_id=? AND excluded=?", d.ID, true).Find(&excludedEntries).Error; err != nil {
			return err
		}
		for _, e := range excludedEntries {
			if _, selected := resources[e.ResourceKey]; selected {
				excluded++
			}
		}
		for _, a := range p.Assignments {
			c := resources[a.ResourceKey]
			if !validCandidate(c) {
				return ErrInput
			}
			var e orm.DocumentDirectoryEntry
			err := tx.Where("directory_id=? AND resource_key=?", d.ID, a.ResourceKey).Take(&e).Error
			fresh := errors.Is(err, gorm.ErrRecordNotFound)
			if err != nil && !fresh {
				return err
			}
			// An older read cannot revise an already accepted classification either.
			if !fresh && c.ReadAt != nil && e.ReadAt != nil && c.ReadAt.Truncate(time.Microsecond).Before(e.ReadAt.Truncate(time.Microsecond)) {
				continue
			}
			hasReadEvidence := c.ReadAt != nil && (c.ReadState == "read" || c.ReadState == "partial") && strings.TrimSpace(c.Excerpt) != "" && c.AccessState != "denied" && c.AccessState != "unavailable" && c.AccessState != "deleted"
			// PostgreSQL stores microseconds; JSON observations may retain nanoseconds.
			newRead := hasReadEvidence && (e.ReadAt == nil || c.ReadAt.Truncate(time.Microsecond).After(e.ReadAt.Truncate(time.Microsecond)))
			readNotOlder := c.ReadAt != nil && (e.ReadAt == nil || !c.ReadAt.Truncate(time.Microsecond).Before(e.ReadAt.Truncate(time.Microsecond)))
			merged := mergeCandidate(Candidate{Source: e.Source, Namespace: e.Namespace, ResourceID: e.ResourceID, Title: e.Title, Locator: e.LocatorJSON, ReadState: e.ReadState, ReadAt: e.ReadAt, AccessState: e.AccessState, ContentVersion: e.ContentVersion}, c)
			versionChanged := merged.ContentVersion != "" && e.ContentVersion != "" && strings.SplitN(merged.ContentVersion, ":", 2)[0] == strings.SplitN(e.ContentVersion, ":", 2)[0] && merged.ContentVersion != e.ContentVersion
			titleChanged := merged.Title != "" && merged.Title != e.Title
			preserveConfirmation := !fresh && e.Confirmed && !newRead && !versionChanged && !titleChanged && e.GroupID == groups[a.GroupID]
			// Metadata can independently support classification without claiming a read.
			// Body-based decisions still require a current, accessible read excerpt.
			supported := hasReadEvidence && readNotOlder && (!versionChanged || newRead)
			if a.EvidenceBasis == "metadata" {
				supported = strings.TrimSpace(c.Title) != "" && strings.TrimSpace(a.Reason) != "" && c.AccessState != "deleted"
			}
			if fresh {
				e = entryFromCandidate(d.ID, c, now)
				added++
			} else if e.Excluded {
				continue
			} else {
				e.Title = merged.Title
				e.LocatorJSON = merged.Locator
				e.AccessState = merged.AccessState
				if merged.ContentVersion != "" && e.ContentVersion != "" && strings.SplitN(merged.ContentVersion, ":", 2)[0] == strings.SplitN(e.ContentVersion, ":", 2)[0] {
					e.ContentState = "unchanged"
					if versionChanged {
						e.ContentState = "changed"
						e.Confirmed = false
					}
				} else if merged.ContentVersion != "" {
					e.ContentState = "unknown"
				}
				e.ContentVersion = merged.ContentVersion
				e.ReadAt, e.ReadState = merged.ReadAt, merged.ReadState
			}
			if !e.Pinned {
				e.GroupID = groups[a.GroupID]
				e.Confirmed = preserveConfirmation || (a.Confirmed && supported)
			}
			if !e.Confirmed {
				pending++
			}
			if !preserveConfirmation && !e.Pinned {
				e.EvidenceJSON = encoded(map[string]any{"batch_id": b.ID, "reason": a.Reason, "evidence_basis": a.EvidenceBasis})
			}
			e.UpdatedAt = now
			if err := tx.Save(&e).Error; err != nil {
				return err
			}
		}
		evicted := ""
		if !d.Ready && !input.Chunked && !input.Reorganize {
			evicted, err = makeRoom(tx, b.UserID, d.ID)
			if err != nil {
				return err
			}
			d.Ready = true
		}
		var coverage []map[string]any
		_ = json.Unmarshal(d.CoverageJSON, &coverage)
		for _, o := range input.Observations {
			var obs Observation
			if json.Unmarshal(o.PayloadJSON, &obs) == nil {
				coverage = append(coverage, map[string]any{"observation_id": o.ID, "query": o.Query, "source": obs.Source, "operation": obs.Operation, "scope": obs.Scope, "status": obs.Status, "incomplete": obs.Incomplete, "at": o.CreatedAt})
			}
		}
		// Recent query-scoped observations are a bounded display history, never
		// a claim that an entire source has been checked.
		if len(coverage) > 100 {
			coverage = coverage[len(coverage)-100:]
		}
		d.CoverageJSON = encoded(coverage)
		d.Version++
		d.OrganizedAt = &now
		d.UpdatedAt = now
		if err := tx.Save(&d).Error; err != nil {
			return err
		}
		b.Status = "succeeded"

		b.UpdatedAt = now
		if input.FolderBatch || input.Chunked {
			b.Status = "running"
			grouped, unclassified, err := classificationCounts(tx, d.ID)
			if err != nil {
				return err
			}
			offset := input.FolderOffset
			if input.Chunked {
				offset = input.ChunkOffset
			}
			var prior struct {
				Added    int `json:"added"`
				Pending  int `json:"pending"`
				Excluded int `json:"excluded"`
			}
			_ = json.Unmarshal(b.ResultJSON, &prior)
			b.ResultJSON = folderResult(b.ResultJSON, map[string]any{"added": prior.Added + added, "pending": prior.Pending + pending, "excluded": prior.Excluded + excluded, "processed": offset, "grouped": grouped, "unclassified": unclassified, "classification_state": "running", "directory_name": d.Name})
		} else {
			b.ResultJSON = batchResult(b.ResultJSON, map[string]any{"added": added, "pending": pending, "excluded": excluded, "evicted": evicted, "directory_name": d.Name, "usage": p.Usage})
		}
		return tx.Save(b).Error
	})
}

func BatchList(ctx context.Context, db *gorm.DB, uid string) ([]orm.DocumentDirectoryBatch, error) {
	rows := []orm.DocumentDirectoryBatch{}
	var rootIDs []string
	if err := db.WithContext(ctx).Model(&orm.DocumentDirectoryBatch{}).Where("user_id=? AND parent_id=?", uid, "").Order("created_at DESC").Limit(20).Pluck("id", &rootIDs).Error; err != nil {
		return nil, err
	}
	if len(rootIDs) == 0 {
		return rows, nil
	}
	if err := db.WithContext(ctx).Where("user_id=? AND (id IN ? OR parent_id IN ?)", uid, rootIDs, rootIDs).Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].Status == "succeeded" || rows[i].Status == "canceled" {
			continue
		}
		var j orm.AsyncJob
		if err := db.WithContext(ctx).Select("status").Where("id=?", rows[i].JobID).Take(&j).Error; err != nil {
			return nil, err
		}
		rows[i].Status = j.Status
	}
	return rows, nil
}

func Cleanup(ctx context.Context, db *gorm.DB) error {
	var rows []orm.DocumentDirectory
	if err := db.WithContext(ctx).Where("deleted_at IS NULL AND pinned=? AND last_used_at<? AND (use_until IS NULL OR use_until<=?)", false, time.Now().UTC().Add(-7*24*time.Hour), time.Now().UTC()).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := mutate(ctx, db, row.UserID, func(tx *gorm.DB) error {
			d, err := requireDirectory(tx, row.UserID, row.ID)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			if d.Pinned || d.LastUsedAt.After(now.Add(-7*24*time.Hour)) || d.UseUntil != nil && d.UseUntil.After(now) {
				return nil
			}
			if b, e := busy(tx, row.UserID, row.ID); e != nil {
				return e
			} else if b {
				return nil
			}
			if using, err := activeUse(tx, row.UserID, row.ID); err != nil {
				return err
			} else if using {
				return nil
			}
			return removeTx(tx, row.UserID, row.ID)
		}); err != nil && !errors.Is(err, ErrDisabled) {
			return err
		}
	}
	return nil
}

func StartCleanup(ctx context.Context, db *gorm.DB) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			if err := Cleanup(ctx, db); err != nil && ctx.Err() == nil {
				log.Logger.Error().Err(err).Msg("document directory cleanup failed")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

// Prevent duplicate callback delivery without changing the original observation.
func SaveObservations(ctx context.Context, db *gorm.DB, uid, conversation, history, run, query string, observations []Observation) error {
	return mutate(ctx, db, uid, func(tx *gorm.DB) error {
		for _, o := range observations {
			if o.CallID == "" || len(o.CallID) > 255 || len(o.Candidates) > 1000 {
				return ErrInput
			}
			for _, c := range o.Candidates {
				if !validCandidate(c) {
					return ErrInput
				}
			}
			row := orm.DocumentObservation{ID: key([]string{uid, conversation, history, run, o.CallID}), UserID: uid, ConversationID: conversation, HistoryID: history, RunID: run, CallID: o.CallID, Query: query, PayloadJSON: encoded(o), CreatedAt: time.Now().UTC()}
			if o.InventoryID != "" || len(o.InventoryIDs) > 0 {
				if err := saveScopedObservation(tx, row, o); err != nil {
					return err
				}
				continue
			}
			conflict := clause.OnConflict{DoNothing: true}
			if o.CallID == "__capture__" {
				conflict = clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"payload_json"}), Where: clause.Where{Exprs: []clause.Expression{clause.Eq{Column: clause.Column{Table: "document_observations", Name: "batch_id"}, Value: ""}}}}
			}
			if err := tx.Clauses(conflict).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Preserve per-attempt accounting when committing the final batch summary.
func batchResult(previous json.RawMessage, result map[string]any) json.RawMessage {
	var old map[string]any
	_ = json.Unmarshal(previous, &old)
	if attempts, ok := old["model_attempts"]; ok {
		result["model_attempts"] = attempts
		result["usage"] = old["usage"]
	}
	return encoded(result)
}
