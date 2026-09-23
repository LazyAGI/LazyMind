package learning

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/doc"
	"lazymind/core/store"
)

func service() *Service { return New(store.DB()) }

var preanalysisCancelFuncs sync.Map

func Catalog(w http.ResponseWriter, _ *http.Request) {
	common.ReplyOK(w, map[string]any{"capabilities": Capabilities(), "question_types": QuestionTypes(), "profiles": BuiltinProfiles(), "local_available": LocalAvailable()})
}
func ListProfiles(w http.ResponseWriter, r *http.Request) {
	rows, err := service().ListProfiles(r.Context(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, map[string]any{"builtin": BuiltinProfiles(), "custom": rows})
}
func CreateProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name, Description string
		Capabilities      []CapabilityRef
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	row, err := service().CreateProfile(r.Context(), store.UserID(r), in.Name, in.Description, in.Capabilities)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, row)
}
func ListKBCapabilities(w http.ResponseWriter, r *http.Request) {
	rows, err := service().ListKnowledgeBaseCapabilities(r.Context(), store.UserID(r), mux.Vars(r)["dataset_id"])
	if err != nil {
		common.ReplyErr(w, err.Error(), 404)
		return
	}
	common.ReplyOK(w, map[string]any{"items": rows})
}
func PutKBCapabilities(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Capabilities []CapabilityRef `json:"capabilities"`
		ProfileKey   string          `json:"profile_key"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if in.ProfileKey != "" {
		refs, err := service().ProfileRefs(r.Context(), store.UserID(r), in.ProfileKey)
		if err != nil {
			common.ReplyErr(w, err.Error(), 400)
			return
		}
		in.Capabilities = refs
	}
	if err := service().PutKnowledgeBaseCapabilities(r.Context(), store.UserID(r), mux.Vars(r)["dataset_id"], in.Capabilities); err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	ListKBCapabilities(w, r)
}
func PutPreset(w http.ResponseWriter, r *http.Request) {
	var raw struct {
		ScopeType        string         `json:"scope_type"`
		ScopeID          string         `json:"scope_id"`
		DocumentRevision string         `json:"document_revision"`
		CapabilityKey    string         `json:"capability_key"`
		Key              string         `json:"key"`
		Value            map[string]any `json:"value"`
		SchemaVersion    int            `json:"schema_version"`
		Priority         int            `json:"priority"`
		Origin           string         `json:"origin"`
	}
	if json.NewDecoder(r.Body).Decode(&raw) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	row, err := service().PutPreset(r.Context(), store.UserID(r), PresetInput(raw))
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, row)
}
func ListPresets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, err := service().ListPresets(r.Context(), store.UserID(r), q.Get("scope_type"), q.Get("scope_id"), q.Get("capability_key"))
	if err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, map[string]any{"items": rows})
}
func UpdatePreset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Value    map[string]any `json:"value"`
		Priority int            `json:"priority"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	row, err := service().UpdatePreset(r.Context(), store.UserID(r), mux.Vars(r)["preset_id"], in.Value, in.Priority)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, row)
}
func DeletePreset(w http.ResponseWriter, r *http.Request) {
	if err := service().DeletePreset(r.Context(), store.UserID(r), mux.Vars(r)["preset_id"]); err != nil {
		common.ReplyErr(w, err.Error(), 404)
		return
	}
	common.ReplyOK(w, map[string]any{"deleted": true})
}
func ResolveContent(w http.ResponseWriter, r *http.Request) {
	var raw struct {
		CapabilityKey    string         `json:"capability_key"`
		Text             string         `json:"text"`
		Context          string         `json:"context"`
		Language         string         `json:"language"`
		SubjectKind      string         `json:"subject_kind"`
		DatasetID        string         `json:"dataset_id"`
		DocumentID       string         `json:"document_id"`
		DocumentRevision string         `json:"document_revision"`
		TargetLanguage   string         `json:"target_language"`
		SegmentID        string         `json:"segment_id"`
		Page             *int           `json:"page"`
		StartOffset      int            `json:"start_offset"`
		EndOffset        int            `json:"end_offset"`
		BookIDs          []string       `json:"book_ids"`
		Preview          bool           `json:"preview"`
		Value            map[string]any `json:"value"`
		Preanalysis      bool           `json:"-"`
	}
	if json.NewDecoder(r.Body).Decode(&raw) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	request := ResolveContentRequest{CapabilityKey: raw.CapabilityKey, Text: raw.Text, Context: raw.Context, Language: raw.Language, SubjectKind: raw.SubjectKind, DatasetID: raw.DatasetID, DocumentID: raw.DocumentID, DocumentRevision: raw.DocumentRevision, TargetLanguage: raw.TargetLanguage, SegmentID: raw.SegmentID, Page: raw.Page, StartOffset: raw.StartOffset, EndOffset: raw.EndOffset, BookIDs: raw.BookIDs, Preview: raw.Preview, ProvidedValue: raw.Value}
	out, err := service().ResolveContent(r.Context(), store.UserID(r), request)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, out)
}

func CreateBook(w http.ResponseWriter, r *http.Request) {
	var raw struct {
		Name             string         `json:"name"`
		Description      string         `json:"description"`
		CapabilityKey    string         `json:"capability_key"`
		QuestionTypes    []string       `json:"question_types"`
		GenerationPolicy map[string]any `json:"generation_policy"`
	}
	if json.NewDecoder(r.Body).Decode(&raw) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	row, err := service().CreateBook(r.Context(), store.UserID(r), Book{Name: raw.Name, Description: raw.Description, CapabilityKey: raw.CapabilityKey, GenerationPolicyJSON: marshal(raw.GenerationPolicy)}, raw.QuestionTypes)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, row)
}
func ListBooks(w http.ResponseWriter, r *http.Request) {
	var rows []Book
	q := store.DB().WithContext(r.Context()).Where("owner_id = ? AND archived_at IS NULL", store.UserID(r))
	if key := r.URL.Query().Get("capability_key"); key != "" {
		q = q.Where("capability_key = ?", key)
	}
	if err := q.Order("created_at").Find(&rows).Error; err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, map[string]any{"items": rows})
}
func UpdateBook(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          string   `json:"name"`
		Description   string   `json:"description"`
		CapabilityKey string   `json:"capability_key"`
		QuestionTypes []string `json:"question_types"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	row, err := service().UpdateBook(r.Context(), store.UserID(r), mux.Vars(r)["book_id"], in.Name, in.Description, in.CapabilityKey, in.QuestionTypes)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, row)
}
func ArchiveBook(w http.ResponseWriter, r *http.Request) {
	if err := service().ArchiveBook(r.Context(), store.UserID(r), mux.Vars(r)["book_id"]); err != nil {
		common.ReplyErr(w, err.Error(), 404)
		return
	}
	common.ReplyOK(w, map[string]any{"archived": true})
}
func ImportDictionary(w http.ResponseWriter, r *http.Request) {
	if err := requireLocal(); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	var in struct {
		ProviderKey   string            `json:"provider_key"`
		SourceName    string            `json:"source_name"`
		SourceVersion string            `json:"source_version"`
		LicenseID     string            `json:"license_id"`
		SourceURL     string            `json:"source_url"`
		Checksum      string            `json:"checksum"`
		Entries       []DictionaryEntry `json:"entries"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Entries) == 0 {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if strings.TrimSpace(in.ProviderKey) == "" || strings.TrimSpace(in.SourceName) == "" || strings.TrimSpace(in.SourceVersion) == "" || strings.TrimSpace(in.LicenseID) == "" || strings.TrimSpace(in.Checksum) == "" {
		common.ReplyErr(w, "dictionary import metadata is required", 400)
		return
	}
	validProvider := in.ProviderKey == "chinese_dictionary" || in.ProviderKey == "chinese_idiom_dictionary" || in.ProviderKey == "classical_chinese_dictionary" || in.ProviderKey == "english_dictionary"
	if !validProvider {
		common.ReplyErr(w, "unsupported dictionary provider", 400)
		return
	}
	checksum := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(in.Checksum)), "sha256:")
	if decoded, decodeErr := hex.DecodeString(checksum); decodeErr != nil || len(decoded) != 32 {
		common.ReplyErr(w, "dictionary checksum must be SHA-256", 400)
		return
	}
	in.Checksum = checksum
	if len(in.Entries) > 5000 {
		common.ReplyErr(w, "too many dictionary entries", 400)
		return
	}
	for i := range in.Entries {
		row := &in.Entries[i]
		if row.ProviderKey == "" {
			row.ProviderKey = in.ProviderKey
		}
		if row.SourceName == "" {
			row.SourceName = in.SourceName
		}
		if row.SourceVersion == "" {
			row.SourceVersion = in.SourceVersion
		}
		if row.LicenseID == "" {
			row.LicenseID = in.LicenseID
		}
		if row.ProviderKey != in.ProviderKey {
			common.ReplyErr(w, "unsupported dictionary provider", 400)
			return
		}
		if strings.TrimSpace(row.DisplayHeadword) == "" || strings.TrimSpace(row.SourceName) == "" || strings.TrimSpace(row.SourceVersion) == "" || strings.TrimSpace(row.LicenseID) == "" || !json.Valid([]byte(row.PayloadJSON)) {
			common.ReplyErr(w, "dictionary provenance and valid payload_json are required", 400)
			return
		}
		row.ID = uuid.NewString()
		row.NormalizedHeadword = normalize(row.DisplayHeadword)
		if row.Priority == 0 {
			row.Priority = 100
		}
	}
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		batch := DictionaryImport{ID: uuid.NewString(), ProviderKey: in.ProviderKey, SourceName: in.SourceName, SourceVersion: in.SourceVersion, LicenseID: in.LicenseID, SourceURL: in.SourceURL, Checksum: in.Checksum, ImportedAt: time.Now().UTC()}
		var existing DictionaryImport
		lookupErr := tx.Where("provider_key = ? AND source_name = ? AND source_version = ?", in.ProviderKey, in.SourceName, in.SourceVersion).First(&existing).Error
		if lookupErr == nil && (existing.Checksum != in.Checksum || existing.LicenseID != in.LicenseID) {
			return errors.New("dictionary import version metadata conflicts with existing import")
		}
		if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return lookupErr
		}
		if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			if err := tx.Create(&batch).Error; err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&in.Entries).Error
	})
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, map[string]any{"imported": len(in.Entries)})
}

func CreateReviewSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BookID string `json:"book_id"`
		Locale string `json:"locale"`
		Limit  int    `json:"limit"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	out, err := service().CreateReviewSession(r.Context(), store.UserID(r), in.BookID, in.Locale, in.Limit)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, out)
}
func GetReviewSession(w http.ResponseWriter, r *http.Request) {
	out, err := service().GetReviewSession(r.Context(), store.UserID(r), mux.Vars(r)["session_id"])
	if err != nil {
		common.ReplyErr(w, err.Error(), 404)
		return
	}
	common.ReplyOK(w, out)
}
func AnswerReviewQuestion(w http.ResponseWriter, r *http.Request) {
	var in struct {
		QuestionID     string `json:"question_id"`
		Response       string `json:"response"`
		Rating         string `json:"rating"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	out, err := service().AnswerQuestion(r.Context(), store.UserID(r), mux.Vars(r)["session_id"], in.QuestionID, in.Response, in.Rating, in.IdempotencyKey)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, out)
}
func CreatePreanalysisTask(w http.ResponseWriter, r *http.Request) {
	var in PreanalysisRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if len(in.Items) == 0 && in.DatasetID != "" && in.DocumentID != "" {
		documentService, err := doc.NewDocumentService(doc.DocumentServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
		if err != nil {
			common.ReplyErr(w, err.Error(), 500)
			return
		}
		chunkRequest := doc.DocumentChunksRequest{UserID: store.UserID(r), DatasetID: in.DatasetID, DocumentID: in.DocumentID, PageSize: 100, SegmentGroup: doc.RootNodeGroup}
		loadChunks := func() error {
			in.Items = nil
			chunkRequest.PageToken = ""
			for {
				result, loadErr := documentService.ListDocumentChunks(r.Context(), chunkRequest)
				if loadErr != nil {
					return loadErr
				}
				for _, chunk := range result.Chunks {
					in.Items = append(in.Items, PreanalysisItem{Text: chunk.Text, Context: chunk.Text, SegmentID: chunk.ID})
				}
				chunkRequest.PageToken = result.NextPageToken
				if chunkRequest.PageToken == "" {
					return nil
				}
			}
		}
		if loadErr := loadChunks(); loadErr != nil {
			common.ReplyErr(w, loadErr.Error(), 400)
			return
		}
		if len(in.Items) == 0 {
			parsed, ensureErr := documentService.EnsureDocumentParsed(r, doc.EnsureDocumentParsedRequest{UserID: store.UserID(r), DatasetID: in.DatasetID, DocumentID: in.DocumentID})
			if ensureErr != nil {
				common.ReplyErr(w, ensureErr.Error(), 400)
				return
			}
			// Parsing is asynchronous. Wait briefly so one click can continue once
			// the canonical Reader root nodes have been persisted.
			deadline := time.NewTimer(30 * time.Second)
			ticker := time.NewTicker(time.Second)
			defer deadline.Stop()
			defer ticker.Stop()
			for len(in.Items) == 0 {
				select {
				case <-r.Context().Done():
					common.ReplyErr(w, "document chunk generation canceled", 408)
					return
				case <-deadline.C:
					common.ReplyErr(w, "document parsing is still running; please retry shortly", 409)
					return
				case <-ticker.C:
					if loadErr := loadChunks(); loadErr != nil {
						common.ReplyErr(w, loadErr.Error(), 400)
						return
					}
				}
			}
			_ = parsed
		}
	}
	task, err := service().CreatePreanalysisTask(r.Context(), store.UserID(r), in)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, task)
}
func RunPreanalysisTask(w http.ResponseWriter, r *http.Request) {
	owner, taskID := store.UserID(r), mux.Vars(r)["task_id"]
	task, err := service().GetPreanalysisTask(r.Context(), owner, taskID)
	if err != nil {
		common.ReplyErr(w, err.Error(), 404)
		return
	}
	if task.Status != "queued" {
		common.ReplyErr(w, "preanalysis task is already running", 409)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	preanalysisCancelFuncs.Store(taskID, cancel)
	go func() {
		defer preanalysisCancelFuncs.Delete(taskID)
		defer cancel()
		_, _ = service().RunPreanalysisTask(ctx, owner, taskID)
	}()
	common.ReplyOK(w, task)
}
func GetPreanalysisTask(w http.ResponseWriter, r *http.Request) {
	task, err := service().GetPreanalysisTask(r.Context(), store.UserID(r), mux.Vars(r)["task_id"])
	if err != nil {
		common.ReplyErr(w, err.Error(), 404)
		return
	}
	common.ReplyOK(w, task)
}
func GetLatestPreanalysisTask(w http.ResponseWriter, r *http.Request) {
	task, err := service().LatestPreanalysisTask(r.Context(), store.UserID(r), r.URL.Query().Get("dataset_id"), r.URL.Query().Get("document_id"))
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, task)
}
func CancelPreanalysisTask(w http.ResponseWriter, r *http.Request) {
	taskID := mux.Vars(r)["task_id"]
	task, err := service().CancelPreanalysisTask(r.Context(), store.UserID(r), taskID)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	if raw, ok := preanalysisCancelFuncs.LoadAndDelete(taskID); ok {
		raw.(context.CancelFunc)()
	}
	common.ReplyOK(w, task)
}

func ListPreanalysisDrafts(w http.ResponseWriter, r *http.Request) {
	out, err := service().ListPreanalysisDrafts(r.Context(), store.UserID(r), mux.Vars(r)["task_id"])
	if err != nil {
		common.ReplyErr(w, err.Error(), 404)
		return
	}
	common.ReplyOK(w, out)
}

func PublishPreanalysisDrafts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PresetIDs        []string `json:"preset_ids"`
		ContentIDs       []string `json:"content_ids"`
		DocumentRevision string   `json:"document_revision"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	out, err := service().PublishPreanalysisDrafts(r.Context(), store.UserID(r), mux.Vars(r)["task_id"], in.DocumentRevision, in.PresetIDs, in.ContentIDs)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, out)
}
