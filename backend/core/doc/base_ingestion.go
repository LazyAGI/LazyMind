package doc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/settings"
	"lazymind/core/store"
)

const baseArtifactSuffix = ".lazymind-base.json"

type baseNode struct {
	ID       string         `json:"id"`
	Text     string         `json:"text"`
	Metadata map[string]any `json:"metadata"`
}
type baseArtifact struct {
	SourceSHA      string     `json:"source_sha256"`
	ParserVersion  string     `json:"parser_version"`
	ChunkerVersion string     `json:"chunker_version"`
	Roots          []baseNode `json:"roots"`
	Chunks         []baseNode `json:"chunks"`
}

func prepareBaseDocument(r *http.Request, datasetID string, task orm.Task) error {
	db := store.DB().WithContext(r.Context())
	controls, err := settings.LoadFeatureControls(r.Context(), db, common.UserID(r))
	if err != nil {
		return err
	}
	if !controls.DocumentParsingEnabled {
		return fmt.Errorf("document parsing is paused in settings")
	}
	var document orm.Document
	if err := db.Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", task.DocID, datasetID).Take(&document).Error; err != nil {
		return err
	}
	initial := newDocumentProcessingState(datasetID, document.ID, time.Now().UTC())
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&initial).Error; err != nil {
		return err
	}
	var ext documentExt
	if err := json.Unmarshal(document.Ext, &ext); err != nil {
		return err
	}
	var result struct {
		ArtifactRef    string `json:"artifact_ref"`
		SourceSHA      string `json:"source_sha256"`
		ParserVersion  string `json:"parser_version"`
		ChunkerVersion string `json:"chunker_version"`
	}
	headers := map[string]string{"X-LazyMind-Internal-Token": strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))}
	err = common.ApiPost(r.Context(), common.ChatServiceEndpoint()+"/internal/documents:prepare",
		map[string]string{"source_path": ext.StoredPath}, headers, &result, 2*time.Minute)
	if err == nil && (result.ArtifactRef != ext.StoredPath+baseArtifactSuffix || result.SourceSHA == "") {
		err = fmt.Errorf("invalid native reader artifact")
	}
	if err != nil {
		db.Model(&orm.DocumentProcessingState{}).Where("dataset_id = ? AND document_id = ? AND parse_artifact_ref = ''", datasetID, task.DocID).
			Updates(map[string]any{"parse_status": "failed", "parse_error_message": err.Error(), "updated_at": time.Now().UTC()})
		return err
	}
	if err := db.Model(&orm.DocumentProcessingState{}).Where("dataset_id = ? AND document_id = ?", datasetID, task.DocID).
		Updates(map[string]any{
			"parse_status": "succeeded", "chunk_status": "succeeded", "index_status": "pending",
			"parse_error_message": "", "chunk_error_message": "", "index_error_message": "",
			"source_fingerprint": result.SourceSHA, "parser_version": result.ParserVersion,
			"chunker_version": result.ChunkerVersion, "parse_fingerprint": result.SourceSHA + ":" + result.ParserVersion,
			"chunk_fingerprint":  result.SourceSHA + ":" + result.ChunkerVersion,
			"parse_artifact_ref": result.ArtifactRef, "chunk_artifact_ref": result.ArtifactRef,
			"index_artifact_ref": "", "index_fingerprint": "", "embedding_version": "",
			"updated_at": time.Now().UTC(),
		}).Error; err != nil {
		return err
	}
	var control taskExt
	json.Unmarshal(task.Ext, &control)
	control.TaskState = string(TaskStateSucceeded)
	control.ErrorMessage = ""
	control.ProcessingLevel = ProcessingLevelChunked
	return db.Model(&orm.Task{}).Where("id = ?", task.ID).Update("ext", mustJSON(control)).Error
}

func bindNativeIndexTask(ctx context.Context, datasetID, documentID, taskID, level string) {
	if level != ProcessingLevelIndexed {
		return
	}
	store.DB().WithContext(ctx).Model(&orm.DocumentProcessingState{}).
		Where("dataset_id = ? AND document_id = ? AND parse_artifact_ref <> ''", datasetID, documentID).
		Update("index_artifact_ref", "core-task:"+taskID)
}

func readBaseArtifact(ctx context.Context, rec documentServiceRecord) (*baseArtifact, error) {
	var state orm.DocumentProcessingState
	if err := store.DB().WithContext(ctx).Where("dataset_id = ? AND document_id = ?", rec.row.DatasetID, rec.row.ID).
		Take(&state).Error; err != nil || state.ParseArtifactRef == "" {
		return nil, nil
	}
	if state.ParseArtifactRef != rec.ext.StoredPath+baseArtifactSuffix {
		return nil, fmt.Errorf("invalid native artifact reference")
	}
	file, err := os.Open(state.ParseArtifactRef)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var artifact baseArtifact
	if err := json.NewDecoder(io.LimitReader(file, 128*1024*1024)).Decode(&artifact); err != nil {
		return nil, err
	}
	source, err := os.Open(rec.ext.StoredPath)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, source); err != nil {
		return nil, err
	}
	if artifact.SourceSHA != hex.EncodeToString(hash.Sum(nil)) {
		return nil, fmt.Errorf("document changed; native parsing must be retried")
	}
	return &artifact, nil
}

func useBaseChunks(ctx context.Context, datasetID, documentID string) bool {
	var state orm.DocumentProcessingState
	return store.DB().WithContext(ctx).Where("dataset_id = ? AND document_id = ?", datasetID, documentID).
		Take(&state).Error == nil && state.ChunkArtifactRef != "" && state.IndexStatus != "succeeded"
}

func baseDocumentChunks(artifact *baseArtifact, pageToken string, pageSize int, group string) (DocumentChunksResult, error) {
	offset, err := parseDatasetPageToken(pageToken)
	if err != nil {
		return DocumentChunksResult{}, err
	}
	nodes := artifact.Chunks
	if group != "" && group != "block" && group != "Chunk" && group != RootNodeGroup {
		return DocumentChunksResult{Chunks: []DocumentChunk{}, TotalSize: 0}, nil
	}
	if group == RootNodeGroup {
		nodes = artifact.Roots
	}
	if offset < 0 || offset > len(nodes) {
		return DocumentChunksResult{}, fmt.Errorf("invalid page_token")
	}
	end := offset + normalizeDocumentChunkPageSize(pageSize)
	if end > len(nodes) {
		end = len(nodes)
	}
	result := DocumentChunksResult{Chunks: []DocumentChunk{}, TotalSize: int32(len(nodes))}
	for index := offset; index < end; index++ {
		node := nodes[index]
		id := node.ID
		if id == "" {
			id = artifact.SourceSHA + ":" + strconv.Itoa(index)
		}
		result.Chunks = append(result.Chunks, DocumentChunk{ID: id, Text: node.Text, Number: int32(index)})
	}
	if end < len(nodes) {
		result.NextPageToken = strconv.Itoa(end)
	}
	return result, nil
}
