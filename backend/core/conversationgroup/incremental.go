package conversationgroup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

type directoryCard struct {
	ID       string                 `json:"id"`
	ShortID  string                 `json:"short_id"`
	Name     string                 `json:"name"`
	Scope    string                 `json:"scope"`
	Kind     string                 `json:"kind"`
	Count    int                    `json:"count"`
	Examples []snapshotConversation `json:"examples,omitempty"`
	Alias    string                 `json:"alias,omitempty"`
	Version  int64                  `json:"version"`
}
type incrementalAssignment struct {
	ID      string `json:"id"`
	GroupID string `json:"group_id"`
}
type candidateOperation struct {
	Op        string   `json:"op"`
	ID        string   `json:"id,omitempty"`
	Name      string   `json:"name,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	SourceIDs []string `json:"source_ids,omitempty"`
	TargetID  string   `json:"target_id,omitempty"`
}

// Reject extra or missing operation fields at the Core/Algorithm boundary.
func (op *candidateOperation) UnmarshalJSON(raw []byte) error {
	type plain candidateOperation
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	required := map[string][]string{"create": {"op", "id", "name", "scope"}, "rename": {"op", "id", "name"}, "update": {"op", "id", "scope"}, "merge": {"op", "source_ids", "target_id", "name", "scope"}}[value.Op]
	if len(required) == 0 || len(fields) != len(required) {
		return errors.New("invalid candidate operation")
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return errors.New("invalid candidate operation")
		}
	}
	*op = candidateOperation(value)
	return nil
}

type incrementalCheckpoint struct {
	GroupIDs        map[string]string   `json:"group_ids"`
	NextGroupNumber int                 `json:"next_group_number"`
	NextOrdinal     int                 `json:"next_ordinal"`
	Cursor          int                 `json:"cursor"`
	Version         int                 `json:"version"`
	Identity        string              `json:"identity"`
	Stage           string              `json:"stage"`
	Pending         *incrementalPending `json:"pending,omitempty"`
	Repair          int                 `json:"repair"`
	BatchSize       int                 `json:"batch_size"`
}
type incrementalPending struct {
	Operations   []candidateOperation    `json:"operations"`
	Assignments  []incrementalAssignment `json:"assignments"`
	Operation    int                     `json:"operation"`
	AuditOrdinal int                     `json:"audit_ordinal"`
}

func directoryTarget(cards map[string]directoryCard, id string) (string, error) {
	seen := map[string]bool{}
	for id != "free" {
		card, ok := cards[id]
		if !ok || seen[id] {
			return "", errors.New("unknown or cyclic candidate target")
		}
		seen[id] = true
		if card.Alias == "" {
			return id, nil
		}
		id = card.Alias
	}
	return id, nil
}
func loadDirectory(db *gorm.DB, runID string, snapshot organizerSnapshot) (map[string]directoryCard, error) {
	cards := map[string]directoryCard{}
	for _, g := range snapshot.Groups {
		cards[g.ID] = directoryCard{ID: g.ID, Name: g.Name, Scope: g.Scope, Kind: "existing", Version: g.Version}
	}
	var rows []orm.ConversationOrganizerCandidate
	if err := db.Where("run_id=?", runID).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		var card directoryCard
		if err := json.Unmarshal(row.Data, &card); err != nil {
			return nil, err
		}
		cards[row.ID] = card
	}
	return cards, nil
}
func sortedCards(cards map[string]directoryCard) []directoryCard {
	out := []directoryCard{}
	for _, card := range cards {
		if card.Alias == "" {
			out = append(out, card)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Simulates one operation; old assignments remain attached to alias sources.
func applyCandidateOperation(cards map[string]directoryCard, op candidateOperation) ([]string, error) {
	bad := func() ([]string, error) { return nil, errors.New("invalid candidate operation") }
	id := op.ID
	if op.Op == "merge" {
		id = op.TargetID
	}
	if op.Op != "create" {
		var err error
		id, err = directoryTarget(cards, id)
		if err != nil {
			return nil, err
		}
	}
	card := cards[id]
	if op.Op != "create" && card.Kind != "candidate" {
		return bad()
	}
	affected := []string{}
	switch op.Op {
	case "create":
		if _, exists := cards[id]; exists || !strings.HasPrefix(id, "cand_") || len(id) > 255 {
			return bad()
		}
		card = directoryCard{ID: id, Kind: "candidate"}
		card.Name, card.Scope = op.Name, op.Scope
	case "rename":
		card.Name = op.Name
	case "update":
		affected = []string{id}
		card.Scope = op.Scope
	case "merge":
		if len(op.SourceIDs) < 2 {
			return bad()
		}
		seen := map[string]bool{}
		card.Count = 0
		for _, source := range op.SourceIDs {
			target, err := directoryTarget(cards, source)
			if err != nil || seen[target] || cards[target].Kind != "candidate" {
				return bad()
			}
			seen[target] = true
			affected = append(affected, target)
			old := cards[target]
			card.Count += old.Count
		}
		if !seen[id] {
			return bad()
		}
		for source := range seen {
			if source != id {
				old := cards[source]
				old.Alias = id
				old.Version++
				cards[source] = old
			}
		}
		card.Name, card.Scope = op.Name, op.Scope
	default:
		return bad()
	}
	if strings.TrimSpace(card.Scope) == "" {
		return bad()
	}
	name, scope, err := validateGroupInput(groupInput{Name: &card.Name, Scope: &card.Scope}, true)
	if err != nil {
		return nil, err
	}
	card.Name, card.Scope = name, scope
	card.Version++
	cards[id] = card
	names := map[string]bool{}
	for _, item := range cards {
		if item.Alias != "" {
			continue
		}
		name := normalizeName(item.Name)
		if names[name] {
			return nil, errors.New("duplicate group name")
		}
		names[name] = true
	}
	return affected, nil
}
func incrementalItems(db *gorm.DB, runID string, cursor, limit int) ([]orm.ConversationOrganizerSnapshotItem, error) {
	var rows []orm.ConversationOrganizerSnapshotItem
	err := db.Where("run_id=? AND preparation_status='done' AND preparation_reason='' AND summary<>''", runID).Where("ordinal>=?", cursor).Order("ordinal").Limit(limit).Find(&rows).Error
	return rows, err
}
func itemConversations(rows []orm.ConversationOrganizerSnapshotItem) []snapshotConversation {
	out := make([]snapshotConversation, 0, len(rows))
	for _, row := range rows {
		out = append(out, snapshotConversation{ID: row.ConversationID, Title: row.Title, Summary: row.Summary})
	}
	return out
}
func saveIncremental(ctx context.Context, db *gorm.DB, run *orm.ConversationOrganizerRun, job asyncjob.Job, cp incrementalCheckpoint, write func(*gorm.DB) error) error {
	raw, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ownedRunUpdate(ctx, tx, run.ID, job, "running", map[string]any{"checkpoint_json": raw, "progress_current": cp.Cursor, "stage": cp.Stage, "version": gorm.Expr("version+1")}); err != nil {
			return err
		}
		if write != nil {
			return write(tx)
		}
		return nil
	})
	if err == nil {
		run.CheckpointJSON = raw
		run.ProgressCurrent = int64(cp.Cursor)
	}
	return err
}
func incrementalProposal(db *gorm.DB, run orm.ConversationOrganizerRun, snapshot organizerSnapshot, cards map[string]directoryCard) (organizerProposal, error) {
	p := organizerProposal{NewGroups: []proposedNewGroup{}, ExistingGroupAssignments: []proposedAssignment{}, FreeConversationIDs: []string{}, UnassignedReasons: map[string]string{}}
	var rows []orm.ConversationOrganizerSnapshotItem
	if err := db.Where("run_id=? AND preparation_reason='' AND summary<>''", run.ID).Order("ordinal").Find(&rows).Error; err != nil {
		return p, err
	}
	members := map[string][]string{}
	for _, row := range rows {
		target, err := directoryTarget(cards, row.Assignment)
		if err != nil {
			return p, err
		}
		members[target] = append(members[target], row.ConversationID)
	}
	for _, card := range sortedCards(cards) {
		ids := members[card.ID]
		if len(ids) == 0 {
			continue
		}
		if card.Kind == "existing" {
			p.ExistingGroupAssignments = append(p.ExistingGroupAssignments, proposedAssignment{card.ID, card.Version, ids})
		} else if len(ids) >= 3 {
			p.NewGroups = append(p.NewGroups, proposedNewGroup{card.ID, card.Name, card.Scope, ids})
		} else {
			p.FreeConversationIDs = append(p.FreeConversationIDs, ids...)
			for _, id := range ids {
				p.UnassignedReasons[id] = "below_min_group_size"
			}
		}
	}
	p.FreeConversationIDs = append(p.FreeConversationIDs, members["free"]...)
	for _, id := range members["free"] {
		p.UnassignedReasons[id] = "no_matching_group"
	}
	return p, validateProposal(run.SnapshotJSON, p)
}

func runIncrementalStep(ctx context.Context, db *gorm.DB, run *orm.ConversationOrganizerRun, job asyncjob.Job, snapshot organizerSnapshot, config map[string]any) (*organizerProposal, error) {
	cp := incrementalCheckpoint{Stage: "organizing", BatchSize: 50}
	if len(run.CheckpointJSON) > 0 {
		if err := json.Unmarshal(run.CheckpointJSON, &cp); err != nil {
			return nil, err
		}
	}
	if cp.BatchSize < 1 || cp.BatchSize > 50 || cp.Cursor < 0 || cp.Cursor > len(snapshot.Conversations) {
		return nil, errors.New("invalid incremental cursor")
	}
	cards, err := loadDirectory(db, run.ID, snapshot)
	if err != nil {
		return nil, err
	}
	if ensureGroupIDs(&cp, cards) {
		if err := saveIncremental(ctx, db, run, job, cp, nil); err != nil {
			return nil, err
		}
	}
	originalCards := map[string]string{}
	for id, card := range cards {
		raw, _ := json.Marshal(card)
		originalCards[id] = string(raw)
	}
	if cp.Cursor == len(snapshot.Conversations) {
		if err := ownedRunUpdate(ctx, db, run.ID, job, "running", map[string]any{"stage": "final"}); err != nil {
			return nil, err
		}
		run.Stage = "final"
		p, err := incrementalProposal(db, *run, snapshot, cards)
		return &p, err
	}
	rows, err := incrementalItems(db, run.ID, cp.NextOrdinal, cp.BatchSize)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("missing incremental batch")
	}
	input := map[string]any{"task_id": run.ID, "snapshot_id": run.ID, "snapshot_hash": run.SnapshotHash, "identity": cp.Identity, "cursor": cp.Cursor, "repair": cp.Repair, "phase": "batch", "conversations": itemConversations(rows), "directory": mappedDirectory(cards, cp)}
	// Rebuild tentative operations from the committed directory. Only audit progress persists.
	if cp.Pending != nil {
		pending := cp.Pending
		for i, op := range pending.Operations {
			// Capture raw assignment buckets before applying this operation's aliases.
			affectedTargets := map[string]bool{}
			sources := []string{op.ID}
			if op.Op == "merge" {
				sources = op.SourceIDs
			}
			if op.Op == "update" || op.Op == "merge" {
				for _, source := range sources {
					target, e := directoryTarget(cards, source)
					if e != nil {
						return nil, e
					}
					affectedTargets[target] = true
				}
			}
			buckets := []string{}
			for id := range cards {
				target, e := directoryTarget(cards, id)
				if e != nil {
					return nil, e
				}
				if affectedTargets[target] {
					buckets = append(buckets, id)
				}
			}
			_, e := applyCandidateOperation(cards, op)
			if e != nil {
				return nil, e
			}
			if i < pending.Operation {
				continue
			}
			if len(buckets) > 0 {
				var audit []orm.ConversationOrganizerSnapshotItem
				if e := db.Where("run_id=? AND assignment IN ? AND ordinal>?", run.ID, buckets, pending.AuditOrdinal).Order("ordinal").Limit(50).Find(&audit).Error; e != nil {
					return nil, e
				}
				if len(audit) > 0 {
					input["phase"] = "audit"
					input["scope"] = op.Scope
					input["conversations"] = itemConversations(audit)
					delete(input, "directory")
					result, e := callOrganizerStream(ctx, db, run, job, input, config)
					if e != nil {
						return nil, e
					}
					if result.Status != "succeeded" {
						return nil, failedOrganizerCall(result)
					}
					if result.Output.Identity != cp.Identity || result.Output.Processed != len(audit) {
						return nil, errors.New("invalid scope audit identity")
					}
					if !result.Output.Accepted {
						cp.Pending = nil
						cp.Repair++
						cp.Stage = "organizing"
						if cp.Repair >= 3 {
							return nil, errors.New("scope audit rejected after repairs")
						}
					} else {
						pending.Operation = i
						pending.AuditOrdinal = audit[len(audit)-1].Ordinal
						cp.Stage = "organizing"
					}
					return nil, saveIncremental(ctx, db, run, job, cp, nil)
				}
			}
			pending.Operation = i + 1
			pending.AuditOrdinal = -1
		}
		// All operations audited. Validate and commit exactly this prefix of the batch.
		assignments := pending.Assignments
		if len(assignments) == 0 || len(assignments) > len(rows) {
			return nil, errors.New("invalid batch partition")
		}
		expected := map[string]snapshotConversation{}
		for _, item := range itemConversations(rows[:len(assignments)]) {
			expected[item.ID] = item
		}
		for _, assignment := range assignments {
			_, ok := expected[assignment.ID]
			if !ok {
				return nil, errors.New("invalid or duplicate assignment")
			}
			delete(expected, assignment.ID)
			target, e := directoryTarget(cards, assignment.GroupID)
			if e != nil {
				return nil, e
			}
			if target != "free" {
				card := cards[target]
				card.Count++
				cards[target] = card
			}
		}
		cp.NextOrdinal = rows[len(assignments)-1].Ordinal + 1
		cp.Cursor += len(assignments)
		cp.Version++
		cp.Pending = nil
		cp.Repair = 0
		cp.BatchSize = 50
		cp.Stage = "organizing"
		return nil, saveIncremental(ctx, db, run, job, cp, func(tx *gorm.DB) error {
			for _, assignment := range assignments {
				result := tx.Model(&orm.ConversationOrganizerSnapshotItem{}).Where("run_id=? AND conversation_id=? AND assignment=''", run.ID, assignment.ID).Update("assignment", assignment.GroupID)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return errLeaseLost
				}
			}
			for id, card := range cards {
				if card.Kind != "candidate" {
					continue
				}
				raw, _ := json.Marshal(card)
				if string(raw) == originalCards[id] {
					continue
				}
				row := orm.ConversationOrganizerCandidate{RunID: run.ID, ID: id, Data: raw}
				if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "run_id"}, {Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"data"})}).Create(&row).Error; err != nil {
					return err
				}
			}
			return nil
		})
	}
	result, err := callOrganizerStream(ctx, db, run, job, input, config)
	if err != nil {
		return nil, err
	}
	if result.Status != "succeeded" {
		if (result.ErrorCode == "input_too_large" || result.ErrorCode == "output_too_large") && cp.BatchSize > 1 {
			cp.BatchSize /= 2
			return nil, saveIncremental(ctx, db, run, job, cp, nil)
		}
		return nil, failedOrganizerCall(result)
	}
	if result.Output.Identity == "" || (cp.Identity != "" && cp.Identity != result.Output.Identity) || result.Output.Processed != len(rows) || len(result.Output.Assignments) != len(rows) {
		return nil, errors.New("invalid incremental identity or length")
	}
	cp.Identity = result.Output.Identity
	groupIDs, nextNumber, mapErr := decodeGroupIDs(&result.Output, cp, cards)
	if mapErr != nil {
		cp.Repair++
		if cp.Repair >= 3 {
			return nil, mapErr
		}
		return nil, saveIncremental(ctx, db, run, job, cp, nil)
	}
	// Validate the whole tentative directory before spending calls on audits.
	for _, op := range result.Output.Operations {
		if _, err := applyCandidateOperation(cards, op); err != nil {
			cp.Repair++
			if cp.Repair >= 3 {
				return nil, err
			}
			return nil, saveIncremental(ctx, db, run, job, cp, nil)
		}
	}
	expected := map[string]bool{}
	for _, row := range rows {
		expected[row.ConversationID] = true
	}
	for _, assignment := range result.Output.Assignments {
		_, targetErr := directoryTarget(cards, assignment.GroupID)
		if !expected[assignment.ID] || targetErr != nil {
			cp.Repair++
			if cp.Repair >= 3 {
				return nil, errors.New("invalid or duplicate assignment")
			}
			return nil, saveIncremental(ctx, db, run, job, cp, nil)
		}
		delete(expected, assignment.ID)
	}
	cp.GroupIDs, cp.NextGroupNumber = groupIDs, nextNumber
	cp.Pending = &incrementalPending{Operations: result.Output.Operations, Assignments: result.Output.Assignments, AuditOrdinal: -1}
	return nil, saveIncremental(ctx, db, run, job, cp, nil)
}
