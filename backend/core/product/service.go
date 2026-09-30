package product

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
	"lazymind/core/workflow/publication"
	workflowstore "lazymind/core/workflow/store"
)

type Repository struct {
	*workflowstore.Repository
	db *gorm.DB
}

func New(db *gorm.DB) *Repository { return &Repository{Repository: workflowstore.New(db), db: db} }

type Command = workflowstore.Command
type Artifact = workflowstore.Artifact
type InputResource = workflowstore.InputResource
type InputBinding = workflowstore.InputBinding
type ControlSettings = workflowstore.ControlSettings

var ErrNotFound = workflowstore.ErrNotFound
var ErrPermissionDenied = workflowstore.ErrPermissionDenied
var ErrSessionConflict = workflowstore.ErrSessionConflict

type repositoryError string

func (e repositoryError) Error() string { return string(e) }
func requestHash(value []byte) string   { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func supportsProject(session orm.WorkflowSession, db *gorm.DB) bool {
	policy, err := publication.Policy(db, session)
	if err != nil {
		return false
	}
	for _, extension := range policy.HostExtensions {
		if extension == "product-project-v1" {
			return true
		}
	}
	return false
}
func initialReady(raw json.RawMessage, bindings []InputBinding) []string {
	var graph graphengine.CompiledStateGraph
	if json.Unmarshal(raw, &graph) != nil {
		return nil
	}
	snapshot := graphengine.RuntimeSnapshot{}
	for _, binding := range bindings {
		snapshot.Materials = append(snapshot.Materials, graphengine.MaterialValue{MaterialID: binding.MaterialID, RevisionID: binding.ID, Valid: true})
	}
	return graphengine.Project(&graph, snapshot).Ready
}
