package displaystate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

// InputRequest is a projection of the pinned workflow's selected question
// artifact, not a second question definition owned by a host adapter.
type InputRequest struct {
	ArtifactID   string                   `json:"artifact_id"`
	Revision     int                      `json:"revision"`
	Slot         string                   `json:"slot"`
	Title        string                   `json:"title"`
	Questions    []map[string]any         `json:"questions"`
	Source       orm.WorkflowSlotRevision `json:"-"`
	Value        map[string]any           `json:"-"`
	DraftVersion int64                    `json:"-"`
}

// PendingInput shares question resolution across the native display and external
// controller. Automatic mode deliberately retains the native no-question policy.
func PendingInput(db *gorm.DB, session orm.WorkflowSession) (*InputRequest, error) {
	external := session.ControllerHost == "external-agent"
	if session.WorkflowRevisionID == "" || (external && session.WorkflowMode != "dynamic") ||
		(!external && session.ControllerHost != "lazymind") {
		return nil, nil
	}
	var revision orm.WorkflowRevision
	if err := db.Where("id = ?", session.WorkflowRevisionID).First(&revision).Error; err != nil {
		return nil, err
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		return nil, err
	}
	checkpoint := graph.Runtime.InputCheckpoint
	if checkpoint == nil {
		return nil, nil
	}
	var steps []orm.WorkflowSessionStep
	if err := db.Where("session_id = ? AND validity <> ?", session.ID, "stale").Order("attempt DESC, created_at DESC").Find(&steps).Error; err != nil {
		return nil, err
	}
	latest := map[string]orm.WorkflowSessionStep{}
	for _, step := range steps {
		if _, exists := latest[step.StepID]; !exists {
			latest[step.StepID] = step
		}
	}
	producer := latest[checkpoint.AfterStep]
	if producer.Status != "succeeded" {
		return nil, nil
	}
	for _, id := range checkpoint.BeforeSteps {
		if step, exists := latest[id]; exists && !step.CreatedAt.Before(producer.CreatedAt) {
			return nil, nil
		}
	}
	var rows []orm.WorkflowSlotRevision
	if err := db.Where("session_id = ? AND slot_id = ? AND selected = ? AND validity <> ?", session.ID, checkpoint.Slot, true, "stale").Order("revision DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		if !external && len(rows) == 0 {
			return nil, nil
		}
		return nil, errors.New("workflow input checkpoint must have one selected question artifact")
	}
	source := rows[0]
	raw := source.ContentSnapshot
	draft := int64(0)
	if source.HumanArtifactID != nil {
		var artifact orm.WorkflowHumanArtifact
		if err := db.Where("id = ? AND session_id = ?", *source.HumanArtifactID, session.ID).First(&artifact).Error; err != nil {
			return nil, err
		}
		raw, draft = artifact.Value, artifact.DraftVersion
	} else if len(raw) == 0 && source.ArtifactSeq != nil {
		var artifact orm.SubAgentArtifact
		if err := db.Where("task_id = ? AND slot = ? AND seq = ? AND hidden = ?", producer.TaskID, source.Slot, *source.ArtifactSeq, false).First(&artifact).Error; err != nil {
			return nil, err
		}
		raw = artifact.Value
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var value map[string]any
	for i := 0; i < 4; i++ {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, err
		}
		if text, ok := decoded.(string); ok {
			raw = []byte(text)
			continue
		}
		value, _ = decoded.(map[string]any)
		if value == nil {
			return nil, errors.New("invalid workflow input checkpoint")
		}
		if _, ok := value["questions"]; ok {
			break
		}
		data, ok := value["data"]
		if !ok {
			data = value["text"]
		}
		raw, _ = json.Marshal(data)
	}
	questionValues, ok := value["questions"].([]any)
	if !ok {
		return nil, errors.New("workflow input checkpoint requires a questions array")
	}
	answer, _ := value["answer_text"].(string)
	// A generated artifact cannot attest that the user answered its questions.
	if strings.TrimSpace(answer) != "" && (!external || source.ChangeSource == "human") {
		return nil, nil
	}
	if !external {
		token, _ := value["checkpoint_token"].(string)
		if token == "" {
			return nil, nil
		}
	}
	if len(questionValues) == 0 {
		return nil, nil
	}
	questions := make([]map[string]any, 0, len(questionValues))
	for index, item := range questionValues {
		question, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("invalid workflow input question")
		}
		if external {
			question = normalizeInputQuestion(question, index)
			if text, _ := question["text"].(string); strings.TrimSpace(text) == "" {
				return nil, errors.New("workflow input question requires text")
			}
		}
		questions = append(questions, question)
	}
	return &InputRequest{ArtifactID: source.ID, Revision: source.Revision, Slot: checkpoint.Slot,
		Title: checkpoint.Title, Questions: questions, Source: source, Value: value, DraftVersion: draft}, nil
}

func normalizeInputQuestion(raw map[string]any, index int) map[string]any {
	question := make(map[string]any, len(raw)+2)
	for key, value := range raw {
		question[key] = value
	}
	if question["text"] == nil {
		question["text"] = question["question"]
	}
	if id, _ := question["id"].(string); strings.TrimSpace(id) == "" {
		question["id"] = fmt.Sprintf("question-%d", index+1)
	}
	if question["choices"] == nil {
		question["choices"] = question["options"]
	}
	kind, _ := question["type"].(string)
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "text"
		if question["options"] != nil {
			kind = "single"
			if question["multi_select"] == true {
				kind = "multiple"
			}
		}
	}
	switch kind {
	case "boolean", "single", "multiple", "text":
	default:
		kind = "text"
	}
	question["type"] = kind
	if choices, ok := question["choices"].([]any); ok {
		labels := []string{}
		for _, choice := range choices {
			if item, ok := choice.(map[string]any); ok {
				for _, key := range []string{"label", "value", "text"} {
					if label, ok := item[key].(string); ok && label != "" {
						choice = label
						break
					}
				}
			}
			if label, ok := choice.(string); ok && strings.TrimSpace(label) != "" {
				labels = append(labels, strings.TrimSpace(label))
			}
		}
		question["choices"] = labels
	}
	return question
}
