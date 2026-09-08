package executor

import (
	"os"

	"github.com/google/uuid"
	"lazymind/core/workflow/artifactfile"
)

// StageArtifacts materializes immutable file content before a database transaction.
// The caller invokes cleanup only after rollback or an idempotent no-write replay.
func StageArtifacts(sessionID string, artifacts []Artifact) ([]Artifact, func(), error) {
	staged := append([]Artifact(nil), artifacts...)
	var directories []string
	cleanup := func() {
		for _, directory := range directories {
			_ = os.RemoveAll(directory)
		}
	}
	for i := range staged {
		staged[i].stagedID = uuid.NewString()
		value, directory, err := artifactfile.Snapshot(sessionID, staged[i].stagedID, staged[i].ContentType, staged[i].Value)
		if directory != "" {
			directories = append(directories, directory)
		}
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		staged[i].Value = value
	}
	return staged, cleanup, nil
}
