// Package publication owns package-declared immutable output publications.
// A publication is an immutable set of revisions, independent of the working selection.
package publication

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/artifactfile"
	"lazymind/core/workflow/graphengine"
)

const EventType = "workflow.published"

func Policy(db *gorm.DB, session orm.WorkflowSession) (graphengine.RuntimePolicy, error) {
	if session.WorkflowRevisionID == "" || !db.Migrator().HasTable(&orm.WorkflowRevision{}) {
		return graphengine.RuntimePolicy{}, nil
	}
	var revision orm.WorkflowRevision
	if err := db.Select("compiled_graph").First(&revision, "id = ?", session.WorkflowRevisionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return graphengine.RuntimePolicy{}, nil
		}
		return graphengine.RuntimePolicy{}, err
	}
	if len(revision.CompiledGraph) == 0 {
		return graphengine.RuntimePolicy{}, nil
	}
	var graph graphengine.CompiledStateGraph
	err := json.Unmarshal(revision.CompiledGraph, &graph)
	return graph.Runtime, err
}
func PublicationEnabled(db *gorm.DB, session orm.WorkflowSession) (bool, error) {
	policy, err := Policy(db, session)
	return policy.TransactionalOutputs, err
}

type Publication struct {
	AttemptID string            `json:"attempt_id"`
	Revisions map[string]string `json:"revisions"`
	Hashes    map[string]string `json:"hashes"`
}

func Latest(db *gorm.DB, sessionID string) (Publication, error) {
	var event orm.WorkflowEvent
	err := db.Where("session_id = ? AND event_type = ?", sessionID, EventType).Order("id DESC").First(&event).Error
	var value Publication
	if err == nil {
		err = json.Unmarshal(event.PayloadJSON, &value)
	}
	return value, err
}

func Record(db *gorm.DB, session orm.WorkflowSession, value Publication) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return db.Create(&orm.WorkflowEvent{SessionID: session.ID, OwnerUserID: session.CreateUserID,
		ContractVersion: "workflow.v1", EventType: EventType, EntityID: value.AttemptID,
		StateVersion: session.StateVersion + 1, PayloadJSON: raw, CreatedAt: time.Now().UTC()}).Error
}

// Bytes accepts only persisted content and managed files whose digest is verified by Inline.
// Legacy workspace paths are handled by the repository's owner-scoped compatibility reader.
func Bytes(db *gorm.DB, revision orm.WorkflowSlotRevision) ([]byte, error) {
	raw := revision.ContentSnapshot
	if revision.HumanArtifactID != nil {
		var artifact orm.WorkflowHumanArtifact
		if err := db.First(&artifact, "id = ?", *revision.HumanArtifactID).Error; err != nil {
			return nil, err
		}
		raw = artifact.Value
	}
	inlined, err := artifactfile.Inline(raw)
	if err != nil {
		return nil, err
	}
	var value any
	if err = json.Unmarshal(inlined, &value); err != nil {
		return nil, err
	}
	for n := 0; n < 4; n++ {
		switch v := value.(type) {
		case string:
			return []byte(v), nil
		case map[string]any:
			if b64, ok := v["content_base64"].(string); ok {
				return base64.StdEncoding.Strict().DecodeString(b64)
			}
			if _, ok := v["path"]; ok {
				return nil, errors.New("WORKFLOW_ARTIFACT_NOT_FROZEN")
			}
			if data, ok := v["data"]; ok && len(v) <= 3 {
				value = data
				continue
			}
			if text, ok := v["text"]; ok && len(v) <= 3 {
				value = text
				continue
			}
		}
		return json.Marshal(value)
	}
	return nil, errors.New("WORKFLOW_PUBLICATION_INVALID")
}

func digest(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

// Publish runs inside the Attempt completion transaction, after output validation.
// Its input bindings freeze the material revisions used by this finalizer. A failed
// finalizer cannot replace this event, and a retry of a terminal Attempt cannot append it twice.
func Publish(db *gorm.DB, session orm.WorkflowSession, attempt orm.WorkflowSessionStep, policy graphengine.PublicationPolicy) error {
	pub := Publication{AttemptID: attempt.ID, Revisions: map[string]string{}, Hashes: map[string]string{}}
	contents := map[string][]byte{}
	runtime, err := Policy(db, session)
	if err != nil {
		return err
	}
	var bindings []orm.WorkflowAttemptInputBinding
	if err := db.Where("attempt_id = ? AND source_type = 'artifact'", attempt.ID).Find(&bindings).Error; err != nil {
		return err
	}
	for _, binding := range bindings {
		if _, inherited := runtime.PublishedInputAliases[binding.MaterialID]; inherited {
			continue
		}
		var row orm.WorkflowSlotRevision
		if err := db.Where("id = ? AND session_id = ?", binding.MaterialRevisionID, session.ID).First(&row).Error; err != nil {
			return err
		}
		if row.Validity != "effective" {
			return errors.New("WORKFLOW_PUBLICATION_INPUT_CHANGED")
		}
		if binding.ContentHash != "" {
			frozen := row.ContentSnapshot
			if row.HumanArtifactID != nil {
				var artifact orm.WorkflowHumanArtifact
				if err := db.First(&artifact, "id = ?", *row.HumanArtifactID).Error; err != nil {
					return err
				}
				frozen = artifact.Value
			}
			if digest(frozen) != strings.TrimPrefix(binding.ContentHash, "sha256:") {
				return errors.New("WORKFLOW_PUBLICATION_INPUT_CHANGED")
			}
		}
		bytes, err := Bytes(db, row)
		if err != nil {
			return err
		}
		pub.Revisions[row.SlotID] = row.ID
		pub.Hashes[row.SlotID] = digest(bytes)
		contents[row.SlotID] = bytes
	}
	var outputs []orm.WorkflowSlotRevision
	if err := db.Where("producer_attempt_id = ? AND validity = 'effective'", attempt.ID).Order("revision ASC").Find(&outputs).Error; err != nil {
		return err
	}
	for _, row := range outputs {
		bytes, err := Bytes(db, row)
		if err != nil {
			return err
		}
		pub.Revisions[row.SlotID] = row.ID
		pub.Hashes[row.SlotID] = digest(bytes)
		contents[row.SlotID] = bytes
	}
	for _, slot := range policy.RequiredSlots {
		if len(contents[slot]) == 0 {
			return fmt.Errorf("WORKFLOW_PUBLICATION_INCOMPLETE: %s", slot)
		}
	}
	return Record(db, session, pub)
}

// ProjectInputs applies declared publication aliases before graph evaluation, so
// projection witnesses and execution bindings have exactly the same source.
func ProjectInputs(db *gorm.DB, session orm.WorkflowSession, snapshot *graphengine.RuntimeSnapshot) error {
	policy, err := Policy(db, session)
	if err != nil || len(policy.PublishedInputAliases) == 0 {
		return err
	}
	pub, err := Latest(db, session.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	for target, source := range policy.PublishedInputAliases {
		id := pub.Revisions[source]
		if id == "" {
			continue
		}
		var revision orm.WorkflowSlotRevision
		if err := db.Where("id = ? AND session_id = ? AND slot_id = ?", id, session.ID, source).First(&revision).Error; err != nil {
			return err
		}
		kept := make([]graphengine.MaterialValue, 0, len(snapshot.Materials)+1)
		for _, value := range snapshot.Materials {
			if value.MaterialID != target {
				kept = append(kept, value)
			}
		}
		snapshot.Materials = append(kept, graphengine.MaterialValue{MaterialID: target, RevisionID: id, Valid: true})
	}
	return nil
}
