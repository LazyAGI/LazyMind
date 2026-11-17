package executor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/artifactfile"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/graphengine"
)

func UsesPaperAgentContract(attempt AttemptContext) bool {
	return attempt.Execution != nil && attempt.Execution.OutputValidator == graphengine.PaperAgentOutputValidator
}

// PaperToolContext resolves only the immutable attempt inputs. The tool caller
// cannot select an owner, storage path, step, permission or different revision.
func PaperToolContext(ctx context.Context, db *gorm.DB, attempt AttemptContext) (map[string]any, error) {
	if !UsesPaperAgentContract(attempt) {
		return nil, controlstore.Reject("PAPER_CONTRACT_REQUIRED", "this revision does not declare paper-agent-v1")
	}
	var session orm.WorkflowSession
	if err := db.WithContext(ctx).Where("id = ?", attempt.SessionID).First(&session).Error; err != nil {
		return nil, err
	}
	values := map[string]any{}
	var resolve func(any) (any, error)
	resolve = func(raw any) (any, error) {
		switch value := raw.(type) {
		case []any:
			result := []any{}
			for _, item := range value {
				decoded, err := resolve(item)
				if err != nil {
					return nil, err
				}
				result = append(result, decoded)
			}
			return result, nil
		case []map[string]any:
			result := []any{}
			for _, item := range value {
				decoded, err := resolve(item)
				if err != nil {
					return nil, err
				}
				result = append(result, decoded)
			}
			return result, nil
		case map[string]any:
			var content []byte
			var name string
			switch value["source_type"] {
			case "input_resource":
				var resource orm.WorkflowInputResource
				if err := db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", value["source_id"], session.CreateUserID).First(&resource).Error; err != nil {
					return nil, err
				}
				hash := fmt.Sprintf("sha256:%x", sha256.Sum256(resource.Content))
				if value["content_hash"] != hash || hash != resource.ContentHash ||
					value["source_revision"] != strconv.FormatInt(resource.Revision, 10) {
					return nil, controlstore.Reject("INPUT_VERSION_CHANGED", "paper input no longer matches its pinned version")
				}
				content, name = resource.Content, resource.Name
			case "artifact":
				var revision orm.WorkflowSlotRevision
				if err := db.WithContext(ctx).Where("id = ? AND session_id = ?", value["source_revision_id"], session.ID).First(&revision).Error; err != nil {
					return nil, err
				}
				if value["source_revision"] != strconv.Itoa(revision.Revision) {
					return nil, controlstore.Reject("INPUT_VERSION_CHANGED", "paper artifact revision changed")
				}
				stored, err := controlstore.ResolveValue(db.WithContext(ctx), revision)
				if err != nil {
					return nil, err
				}
				if value["content_hash"] != fmt.Sprintf("sha256:%x", sha256.Sum256(stored)) {
					return nil, controlstore.Reject("INPUT_VERSION_CHANGED", "paper artifact content changed")
				}
				inlined, err := artifactfile.Inline(stored)
				if err != nil {
					return nil, err
				}
				var decoded any
				if err := json.Unmarshal(inlined, &decoded); err != nil {
					return nil, err
				}
				return decoded, nil
			default:
				return nil, controlstore.Reject("INPUT_REFERENCE_REQUIRED", "paper tools require Core-pinned input references")
			}
			if len(content) == 0 || len(content) > 20<<20 {
				return nil, controlstore.Reject("INVALID_INPUT", "paper input must be nonempty and at most 20 MiB")
			}
			return map[string]any{"filename": name, "__file_base64": base64.StdEncoding.EncodeToString(content)}, nil
		default:
			return nil, controlstore.Reject("INPUT_REFERENCE_REQUIRED", "paper tools cannot resolve an unbound input")
		}
	}
	for slot, input := range attempt.Inputs {
		value, err := resolve(input)
		if err != nil {
			return nil, err
		}
		values[slot] = value
	}
	var row orm.WorkflowSessionStep
	if err := db.WithContext(ctx).Where("id = ?", attempt.AttemptID).First(&row).Error; err != nil {
		return nil, err
	}
	availableTools := []string{}
	for _, tool := range attempt.Execution.Tools {
		if tool.Source == "package" {
			availableTools = append(availableTools, tool.Name)
		}
	}
	leaseHash := fmt.Sprintf("%x", sha256.Sum256([]byte(row.LeaseToken)))
	return map[string]any{"session_id": attempt.SessionID, "attempt_id": attempt.AttemptID,
		"revision": attempt.WorkflowRevision, "step_id": attempt.StepID, "bindings": attempt.Inputs,
		"request": attempt.UserRequest, "launch_request": attempt.LaunchRequest, "values": values, "lease_hash": leaseHash, "check_liveness": true, "available_tools": availableTools, "knowledge_scope": attempt.KnowledgeScope}, nil
}

func validatePaperCompletion(ctx context.Context, db *gorm.DB, attempt AttemptContext, rows []orm.WorkflowSlotRevision) error {
	scope, err := PaperToolContext(ctx, db, attempt)
	if err != nil {
		return err
	}
	artifacts := []map[string]any{}
	for _, row := range rows {
		raw, err := controlstore.ResolveValue(db.WithContext(ctx), row)
		if err != nil {
			return err
		}
		raw, err = artifactfile.Inline(raw)
		if err != nil {
			return err
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		contentType := "file"
		if row.SlotID == attempt.StepID+"_receipt" {
			contentType = "json"
		}
		seq := 1
		if row.ArtifactSeq != nil {
			seq = *row.ArtifactSeq
		}
		artifacts = append(artifacts, map[string]any{"slot": row.SlotID, "seq": seq, "content_type": contentType, "value": value})
	}
	var result struct {
		Valid bool `json:"valid"`
	}
	err = common.ApiPost(ctx, common.JoinURL(common.ChatServiceEndpoint(), "/api/workflow/paper/verify"),
		map[string]any{"context": scope, "artifacts": artifacts}, map[string]string{"X-LazyMind-Internal-Token": os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")}, &result, 20*time.Second)
	if err != nil {
		return controlstore.Reject("PAPER_OUTPUT_INVALID", "paper candidate verification failed; keep prior artifacts and repair the candidate")
	}
	if !result.Valid {
		return controlstore.Reject("PAPER_OUTPUT_INVALID", "paper candidate does not match its validated receipt")
	}
	return nil
}

// FreezePaperInputs runs inside the claim transaction, after the lease is assigned.
func FreezePaperInputs(ctx context.Context, tx *gorm.DB, attemptID string) error {
	value, err := (DBContextLoader{DB: tx}).LoadAttemptContext(ctx, attemptID)
	if err != nil {
		return err
	}
	if !UsesPaperAgentContract(value) {
		return nil
	}
	return FreezeControlledInputs(ctx, tx, attemptID)
}
