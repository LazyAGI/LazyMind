package doc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"lazymind/core/common"
	"lazymind/core/store"
)

func loadPDFTranslationDraft(ext documentExt, artifactID string) (*pdfArtifactRecord, pdfTranslationDraft, error) {
	artifact := findArtifact(ext, artifactID)
	if artifact == nil || artifact.Kind != pdfArtifactTranslation {
		return nil, pdfTranslationDraft{}, errors.New("translation artifact not found")
	}
	if artifact.DraftPath == "" {
		return nil, pdfTranslationDraft{}, errors.New("translation draft not found; regenerate this translation first")
	}
	raw, err := os.ReadFile(artifact.DraftPath)
	if err != nil {
		return nil, pdfTranslationDraft{}, fmt.Errorf("read translation draft: %w", err)
	}
	var draft pdfTranslationDraft
	if err := json.Unmarshal(raw, &draft); err != nil {
		return nil, pdfTranslationDraft{}, fmt.Errorf("decode translation draft: %w", err)
	}
	return artifact, draft, nil
}

func GetPDFTranslationDraft(w http.ResponseWriter, r *http.Request) {
	_, ext, _, ok := loadPDFDocument(r, false)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", http.StatusNotFound)
		return
	}
	_, draft, err := loadPDFTranslationDraft(ext, strings.TrimSpace(common.PathVar(r, "artifact")))
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusNotFound)
		return
	}
	common.ReplyOK(w, draft)
}

func RetranslatePDFDraftBlock(w http.ResponseWriter, r *http.Request) {
	_, ext, _, ok := loadPDFDocument(r, true)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", http.StatusNotFound)
		return
	}
	artifact, draft, err := loadPDFTranslationDraft(ext, strings.TrimSpace(common.PathVar(r, "artifact")))
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusNotFound)
		return
	}
	var req struct {
		BlockID      string `json:"block_id"`
		ProviderType string `json:"provider_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.BlockID) == "" {
		common.ReplyErr(w, "block_id is required", http.StatusBadRequest)
		return
	}
	providerType := strings.ToLower(strings.TrimSpace(req.ProviderType))
	if providerType == "" {
		providerType = artifact.ProviderType
	}
	if providerType != "api" && providerType != "llm" {
		common.ReplyErr(w, "unsupported translation provider", http.StatusBadRequest)
		return
	}
	var block *pdfTranslationDraftBlock
	for i := range draft.Blocks {
		if draft.Blocks[i].ID == req.BlockID {
			block = &draft.Blocks[i]
			break
		}
	}
	if block == nil {
		common.ReplyErr(w, "translation draft block not found", http.StatusNotFound)
		return
	}
	translated, err := translateDocumentChunk(r.Context(), store.UserID(r), providerType, block.SourceText, draft.TargetLanguage)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadGateway)
		return
	}
	common.ReplyOK(w, map[string]any{"block_id": block.ID, "translated_text": translated})
}

func copyTranslationFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func RevisePDFTranslation(w http.ResponseWriter, r *http.Request) {
	row, ext, _, ok := loadPDFDocument(r, true)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", http.StatusNotFound)
		return
	}
	base, draft, err := loadPDFTranslationDraft(ext, strings.TrimSpace(common.PathVar(r, "artifact")))
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusNotFound)
		return
	}
	var req struct {
		Overrides map[string]string `json:"overrides"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Overrides) == 0 {
		common.ReplyErr(w, "at least one translation override is required", http.StatusBadRequest)
		return
	}
	translations := make(map[string]string, len(draft.Blocks))
	known := make(map[string]bool, len(draft.Blocks))
	for i := range draft.Blocks {
		known[draft.Blocks[i].ID] = true
		translations[draft.Blocks[i].ID] = draft.Blocks[i].TranslatedText
	}
	for id, value := range req.Overrides {
		if !known[id] || strings.TrimSpace(value) == "" {
			common.ReplyErr(w, "invalid translation override", http.StatusBadRequest)
			return
		}
		translations[id] = strings.TrimSpace(value)
	}
	if base.SourcePath == "" || base.LayoutPath == "" {
		common.ReplyErr(w, "translation source or layout is unavailable", http.StatusUnprocessableEntity)
		return
	}
	artifactID := uuid.NewString()
	dir := filepath.Dir(base.StoredPath)
	prefix := filepath.Join(dir, artifactID)
	sourcePath := prefix + ".source.pdf"
	layoutPath := prefix + ".input-layout.json"
	translationsPath := prefix + ".translations.json"
	draftPath := prefix + ".draft.json"
	outputPath := prefix + ".translated.pdf"
	cleanup := func() {
		for _, path := range []string{sourcePath, layoutPath, translationsPath, draftPath, outputPath} {
			_ = os.Remove(path)
		}
	}
	if err := copyTranslationFile(base.SourcePath, sourcePath); err != nil {
		cleanup()
		common.ReplyErr(w, "copy translation source failed", http.StatusInternalServerError)
		return
	}
	if err := copyTranslationFile(base.LayoutPath, layoutPath); err != nil {
		cleanup()
		common.ReplyErr(w, "copy translation layout failed", http.StatusInternalServerError)
		return
	}
	rawTranslations, _ := json.Marshal(translations)
	if err := os.WriteFile(translationsPath, rawTranslations, 0o640); err != nil {
		cleanup()
		common.ReplyErr(w, "save translation overrides failed", http.StatusInternalServerError)
		return
	}
	for i := range draft.Blocks {
		draft.Blocks[i].TranslatedText = translations[draft.Blocks[i].ID]
	}
	draft.Version++
	draft.ArtifactID, draft.BaseArtifactID = artifactID, base.ID
	draft.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writePDFTranslationDraft(draftPath, draft); err != nil {
		cleanup()
		common.ReplyErr(w, "save translation draft failed", http.StatusInternalServerError)
		return
	}
	serviceURL := strings.Replace(strings.TrimSpace(os.Getenv("LAZYMIND_OFFICE_CONVERT_URL")), "/v1/office/to-pdf", "/v1/pdf/render-translation", 1)
	var renderResult struct {
		WarningCount        int `json:"warning_count"`
		RenderedBlockCount  int `json:"rendered_block_count"`
		RequestedBlockCount int `json:"requested_block_count"`
	}
	if serviceURL == "" {
		cleanup()
		common.ReplyErr(w, "PDF translation renderer is not configured", http.StatusServiceUnavailable)
		return
	}
	if err := common.ApiPost(r.Context(), serviceURL, map[string]string{"source_path": sourcePath, "layout_path": layoutPath,
		"translations_path": translationsPath, "output_path": outputPath, "target_language": draft.TargetLanguage}, nil, &renderResult, 15*time.Minute); err != nil {
		cleanup()
		common.ReplyErr(w, err.Error(), http.StatusBadGateway)
		return
	}
	if renderResult.RequestedBlockCount == 0 || renderResult.RenderedBlockCount != renderResult.RequestedBlockCount {
		cleanup()
		common.ReplyErr(w, "PDF translation renderer did not render every block", http.StatusInternalServerError)
		return
	}
	filename := strings.TrimSuffix(base.Filename, filepath.Ext(base.Filename)) + "-revised.pdf"
	artifact := pdfArtifactRecord{ID: artifactID, Kind: pdfArtifactTranslation, CacheKey: base.CacheKey + ":revision:" + artifactID,
		StoredPath: outputPath, SourcePath: sourcePath, LayoutPath: layoutPath, DraftPath: draftPath, HasLayout: true, HasDraft: true,
		Filename: filename, ContentType: mime.TypeByExtension(".pdf"), TargetLanguage: base.TargetLanguage,
		ProviderType: base.ProviderType, Provider: base.Provider, Model: base.Model, WarningCount: renderResult.WarningCount,
		CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	ext.PDFArtifacts = append(ext.PDFArtifacts, artifact)
	rawExt, err := json.Marshal(ext)
	if err != nil || store.DB().WithContext(r.Context()).Model(&row).Update("ext", rawExt).Error != nil {
		cleanup()
		common.ReplyErr(w, "register revised translation failed", http.StatusInternalServerError)
		return
	}
	_ = os.Remove(translationsPath)
	common.ReplyOK(w, artifact)
}
