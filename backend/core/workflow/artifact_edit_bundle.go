package workflow

// Completed artifact edits reuse the same revision, ordering and publication
// primitives as the native editor. A package opts in with its own edit policy.
import (
	"context"
	"encoding/json"
	"fmt"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflow/graphengine"
	"lazymind/core/workflow/publication"
	"net/http"
	"os"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	workflowstore "lazymind/core/workflow/store"
)

type artifactEditPolicy struct {
	Step              string   `yaml:"step" json:"step"`
	Slots             []string `yaml:"slots" json:"slots"`
	LinkedLists       []string `yaml:"linked_lists" json:"linked_lists"`
	Required          []string `yaml:"required" json:"required"`
	RequiredIfPresent []string `yaml:"required_if_present" json:"required_if_present"`
	Instruction       string   `yaml:"instruction" json:"instruction"`
}
type artifactEditItem struct {
	ListIndex   *int            `json:"list_index,omitempty"`
	ContentType string          `json:"content_type,omitempty"`
	Value       json.RawMessage `json:"value,omitempty"`
}
type artifactEditBundle struct {
	RevisionID   string                        `json:"revision_id"`
	StateVersion int64                         `json:"state_version"`
	Slots        map[string][]artifactEditItem `json:"slots"`
}

func editPackage(ctx context.Context, db *gorm.DB, session *orm.WorkflowSession, revision string) (workflowstore.WorkflowPackage, artifactEditPolicy, error) {
	ref := session.WorkflowRef
	if ref == "" {
		ref = session.WorkflowID
	}
	pkg, err := workflowstore.New(db).GetWorkflowPackage(ctx, session.CreateUserID, ref, revision)
	var manifest struct {
		Edit artifactEditPolicy `yaml:"artifact_edit"`
	}
	if err == nil {
		err = yaml.Unmarshal(pkg.Files["workflow.yaml"], &manifest)
	}
	if err == nil && len(manifest.Edit.Slots) == 0 {
		err = fmt.Errorf("workflow package does not support completed artifact edits; update the package")
	}
	return pkg, manifest.Edit, err
}
func GetArtifactEditContext(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	session, err := GetSession(r.Context(), db, common.PathVar(r, "session_id"))
	if err != nil || session.Status != "completed" {
		common.ReplyErr(w, "complete or resume the workflow before editing", 409)
		return
	}
	pkg, policy, err := editPackage(r.Context(), db, session, "")
	if err != nil {
		common.ReplyErr(w, err.Error(), 422)
		return
	}
	rows, err := LoadSelectedSlots(r.Context(), db, session.ID)
	if err != nil {
		common.ReplyErr(w, "cannot load artifacts", 500)
		return
	}
	refs := []map[string]any{}
	for _, row := range rows {
		refs = append(refs, map[string]any{"artifact_id": row.ID, "slot": row.SlotID, "list_index": row.ListIndex, "revision": row.Revision, "validity": row.Validity})
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(pkg.CompiledGraph, &graph); err != nil {
		common.ReplyErr(w, "invalid edit package", 422)
		return
	}
	node, ok := graph.Nodes[policy.Step]
	if !ok || node.Execution == nil {
		common.ReplyErr(w, "edit Skill contract unavailable", 422)
		return
	}
	skills, err := executor.LoadRevisionSkillFiles(r.Context(), db, pkg.RevisionID, node.Execution.Skills)
	if err != nil || len(skills) == 0 {
		common.ReplyErr(w, "edit Skills unavailable", 422)
		return
	}

	common.ReplyOK(w, map[string]any{"session_id": session.ID, "state_version": session.StateVersion, "revision_id": pkg.RevisionID, "policy": policy, "artifacts": refs, "skills": skills, "step_prompt": node.Prompt, "constraints": node.Execution.Constraints, "agent_instruction": "Follow the edit policy and supplied Skills. Read current artifacts by ID. Page numbers mean visual order in this list, not list_index. Preserve unspecified pages. Submit one complete bundle via workflow.edit.commit; do not rewind for a local edit. Existing items without value are preserved; omit an item to delete it, omit list_index on a new item to insert it. Lists in a bundle are the full desired order. Include all nonempty linked lists and update files required by the policy. Validate page count, notes and consistency of the representations being edited before committing."})
}
func CommitArtifactEdit(w http.ResponseWriter, r *http.Request) {
	var input artifactEditBundle
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&input) != nil || input.RevisionID == "" || len(input.Slots) == 0 {
		common.ReplyErr(w, "invalid edit bundle", 400)
		return
	}
	db := store.DB()
	id := common.PathVar(r, "session_id")
	session, err := GetSession(r.Context(), db, id)
	if err != nil {
		common.ReplyErr(w, "session not found", 404)
		return
	}
	_, policy, err := editPackage(r.Context(), db, session, input.RevisionID)
	if err != nil {
		common.ReplyErr(w, err.Error(), 422)
		return
	}
	err = commitArtifactEditBundle(r.Context(), db, id, input, policy)
	if err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	NotifyWorkflowRuntimeUpdated(r.Context(), db, id, "", "artifact_edit")
	common.ReplyOK(w, map[string]any{"status": "committed", "session_id": id})
}
func commitArtifactEditBundle(ctx context.Context, db *gorm.DB, id string, input artifactEditBundle, policy artifactEditPolicy) error {
	directories := []string{}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		session, err := lockArtifactMutationSession(tx, id)
		if err != nil {
			return err
		}
		if session.Status != "completed" || session.StateVersion != input.StateVersion {
			return fmt.Errorf("artifact version changed; reload edit context")
		}
		if tx.Migrator().HasTable(&orm.WorkflowActionJob{}) {
			var pending int64
			if err := tx.Model(&orm.WorkflowActionJob{}).Where("session_id = ? AND status IN ?", id, []string{"waiting_agent", "ready"}).Count(&pending).Error; err != nil {
				return err
			}
			if pending > 0 {
				return fmt.Errorf("confirm or cancel the pending AI preview before a chat edit")
			}
		}
		rows, err := LoadSelectedSlots(ctx, tx, id)
		if err != nil {
			return err
		}
		grouped := map[string][]orm.WorkflowSlotRevision{}
		for _, row := range rows {
			grouped[row.SlotID] = append(grouped[row.SlotID], row)
		}
		allowed := map[string]bool{}
		for _, slot := range policy.Slots {
			allowed[slot] = true
		}
		required := append([]string(nil), policy.Required...)
		for _, slot := range policy.RequiredIfPresent {
			if len(grouped[slot]) > 0 {
				required = append(required, slot)
			}
		}
		for _, slot := range required {
			items := input.Slots[slot]
			if len(items) != 1 || len(items[0].Value) == 0 {
				return fmt.Errorf("updated %s required", slot)
			}
		}
		count := -1
		for _, slot := range policy.LinkedLists {
			if len(grouped[slot]) == 0 {
				continue
			}
			items, ok := input.Slots[slot]
			if !ok {
				return fmt.Errorf("linked list %s required", slot)
			}
			if len(items) == 0 {
				return fmt.Errorf("cannot remove every page")
			}
			if count >= 0 && count != len(items) {
				return fmt.Errorf("linked page counts differ")
			}
			count = len(items)
		}
		// Validate every slot and reference before writing any revision.
		for slot, items := range input.Slots {
			old := grouped[slot]
			if !allowed[slot] || len(old) == 0 {
				return fmt.Errorf("slot %s is not editable", slot)
			}
			single := old[0].ListIndex == nil
			if single && (len(items) != 1 || items[0].ListIndex != nil) {
				return fmt.Errorf("invalid single slot %s", slot)
			}
			seen := map[int]bool{}
			for _, item := range items {
				if item.ListIndex != nil {
					if seen[*item.ListIndex] {
						return fmt.Errorf("duplicate item")
					}
					seen[*item.ListIndex] = true
					found := false
					for _, row := range old {
						if row.ListIndex != nil && *row.ListIndex == *item.ListIndex {
							found = true
						}
					}
					if !found {
						return fmt.Errorf("unknown item in %s", slot)
					}
				} else if !single && len(item.Value) == 0 {
					return fmt.Errorf("new item needs a value")
				}
				if len(item.Value) > 0 && (item.ContentType == "" || string(item.Value) == "null") {
					return fmt.Errorf("content_type and non-null value required")
				}
			}
		}
		for _, slot := range policy.Slots {
			items, included := input.Slots[slot]
			if !included {
				continue
			}
			old := grouped[slot]
			template := old[0]
			cardinality := "single"
			if template.ListIndex != nil {
				cardinality = "list"
			}
			keep := map[int]bool{}
			order := []int{}
			for _, item := range items {
				current := template
				var expected *int
				var draft *int64
				if cardinality == "single" || item.ListIndex != nil {
					for _, row := range old {
						if cardinality == "single" || (row.ListIndex != nil && *row.ListIndex == *item.ListIndex) {
							current = row
							break
						}
					}
					expected = &current.Revision
					if current.HumanArtifactID != nil {
						var human orm.WorkflowHumanArtifact
						if err := tx.First(&human, "id = ?", *current.HumanArtifactID).Error; err != nil {
							return err
						}
						draft = &human.DraftVersion
					}
				}
				index := item.ListIndex
				if len(item.Value) > 0 {
					frozen, directory, err := publication.SnapshotEdit(tx, id, item.ContentType, item.Value)
					if err != nil {
						return err
					}
					if directory != "" {
						directories = append(directories, directory)
					}
					revised, err := WriteSlotRevisionWithHumanArtifact(ctx, tx, id, slot, template.Slot, template.StepID, template.Attempt, cardinality, index, item.ContentType, frozen, nil, "human", expected, draft)
					if err != nil {
						return err
					}
					index = revised.ListIndex
				}
				if index != nil {
					keep[*index] = true
					order = append(order, *index)
				}
			}
			if cardinality == "list" {
				for _, row := range old {
					if !keep[*row.ListIndex] {
						if err := HideSlotItem(ctx, tx, id, slot, *row.ListIndex); err != nil {
							return err
						}
					}
				}
				saved, err := GetSlotOrder(ctx, tx, id, slot)
				if err != nil {
					return err
				}
				version := 0
				if saved != nil {
					version = saved.OrderVersion
				}
				if err := ReorderSlots(ctx, tx, id, []SlotOrderChange{{SlotID: slot, Order: order, Version: version}}); err != nil {
					return err
				}
			}
		}
		// The worker validated these representations as a single new revision.
		// Dependencies outside the bundle retain the normal stale marking.
		names := []string{}
		for slot := range input.Slots {
			names = append(names, slot)
		}
		return tx.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ? AND selected = ? AND slot_id IN ?", id, true, names).Update("validity", "effective").Error
	})
	if err != nil {
		for _, directory := range directories {
			_ = os.RemoveAll(directory)
		}
	}
	return err
}
