package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

const extractorVersion = "bibliography-rules-v2-native-regions"

var numberedReferenceStartPattern = regexp.MustCompile(`(?m)^\s*(?:[-•]\s*)?(?:\[(\d{1,4})\]|(\d{1,4})[.)])\s+`)
var bulletReferenceStartPattern = regexp.MustCompile(`(?m)^\s*([-•])\s+`)

func caller(r *http.Request) doc.DatasetCatalogCaller {
	return doc.DatasetCatalogCaller{
		UserID: store.UserID(r), Authorization: r.Header.Get("Authorization"),
		TenantID: r.Header.Get("X-Tenant-Id"), UserRole: r.Header.Get("X-User-Role"),
	}
}

func replyError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": message})
}

func requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		replyError(w, http.StatusBadRequest, "USER_REQUIRED", "missing X-User-Id")
		return "", false
	}
	return userID, true
}

func parseJSON(r *http.Request, out any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(out) == nil
}

func accessibleDatasets(ctx context.Context, r *http.Request, userID string) (map[string]doc.Dataset, error) {
	service, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: store.DB()})
	if err != nil {
		return nil, err
	}
	out := map[string]doc.Dataset{}
	offset := 0
	for {
		page, err := service.ListDatasets(ctx, doc.DatasetListRequest{UserID: userID, Offset: offset, Limit: 100, Caller: caller(r)})
		if err != nil {
			return nil, err
		}
		for _, item := range page.Datasets {
			out[item.DatasetID] = item
		}
		if !page.HasMore {
			break
		}
		offset = page.NextOffset
	}
	return out, nil
}

func canWriteDataset(ctx context.Context, datasetID, userID string) bool {
	var ds orm.Dataset
	if err := store.DB().WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).Take(&ds).Error; err != nil {
		return false
	}
	return ds.CreateUserID == userID || acl.Can(userID, acl.ResourceTypeDB, datasetID, acl.PermissionDatasetUpload)
}

func documentDataset(ctx context.Context, documentID string) (string, error) {
	var row orm.Document
	err := store.DB().WithContext(ctx).Select("dataset_id").Where("id = ? AND deleted_at IS NULL", documentID).Take(&row).Error
	return row.DatasetID, err
}

func documentText(ctx context.Context, r *http.Request, userID, datasetID, documentID string) (string, error) {
	text, _, err := documentTextWithLayout(ctx, r, userID, datasetID, documentID)
	return text, err
}

func documentTextWithLayout(ctx context.Context, r *http.Request, userID, datasetID, documentID string) (string, []doc.DocumentLayoutBlock, error) {
	service, err := doc.NewDocumentService(doc.DocumentServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
	if err != nil {
		return "", nil, err
	}
	var chunks []string
	var layoutBlocks []doc.DocumentLayoutBlock
	token := ""
	for pages := 0; pages < 100; pages++ {
		result, readErr := service.ListDocumentChunks(ctx, doc.DocumentChunksRequest{UserID: userID, DatasetID: datasetID, DocumentID: documentID, PageToken: token, PageSize: 200, SegmentGroup: doc.RootNodeGroup, Caller: caller(r)})
		if readErr != nil {
			break
		}
		for _, chunk := range result.Chunks {
			if strings.TrimSpace(chunk.Text) != "" {
				chunks = append(chunks, chunk.Text)
			}
			if chunk.LayoutBlocks != nil {
				layoutBlocks = append(layoutBlocks, (*chunk.LayoutBlocks)...)
			}
		}
		token = result.NextPageToken
		if token == "" {
			break
		}
	}
	if len(chunks) > 0 {
		return strings.Join(chunks, "\n"), layoutBlocks, nil
	}
	content, err := service.ReadDocumentContent(ctx, doc.DocumentContentRequest{UserID: userID, DatasetID: datasetID, DocumentID: documentID, Caller: caller(r)})
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(content.Text) == "" {
		parsed, ensureErr := service.EnsureDocumentParsed(r, doc.EnsureDocumentParsedRequest{UserID: userID, DatasetID: datasetID, DocumentID: documentID, Caller: caller(r)})
		if ensureErr != nil {
			return "", nil, ensureErr
		}
		if parsed.Status == "parsing" {
			return "", nil, errors.New("document parsing has started; parsed root nodes are not ready yet")
		}
		return "", nil, errors.New("parsed PDF root nodes are not available yet")
	}
	return content.Text, nil, nil
}

func referenceLayout(raw string, blocks []doc.DocumentLayoutBlock) (int, json.RawMessage) {
	needle := strings.Join(strings.Fields(raw), " ")
	if len(needle) < 12 {
		return 0, emptyJSON("[]")
	}
	for _, block := range blocks {
		haystack := strings.Join(strings.Fields(block.Text), " ")
		start := strings.Index(haystack, needle)
		if start < 0 || len(block.BBox) != 4 {
			continue
		}
		bbox := append([]float64(nil), block.BBox...)
		// Readers sometimes group several bibliography entries into one list
		// node. Split that node proportionally so each reference owns its own
		// hit region instead of the entire list block.
		if len(haystack) > len(needle) {
			height := bbox[3] - bbox[1]
			bbox[1] += height * float64(start) / float64(len(haystack))
			bbox[3] = bbox[1] + height*float64(len(needle))/float64(len(haystack))
		}
		encoded, _ := json.Marshal(bbox)
		return block.Page, encoded
	}
	return 0, emptyJSON("[]")
}

type referenceRegion struct {
	Page int       `json:"page"`
	BBox []float64 `json:"bbox"`
}

type referenceRegionResult struct {
	Index   int               `json:"index"`
	Regions []referenceRegion `json:"regions"`
}

func nativeReferenceRegions(ctx context.Context, documentID string, references []extractedReference) map[int][]referenceRegion {
	var row orm.Document
	if err := store.DB().WithContext(ctx).Select("ext").Where("id = ?", documentID).Take(&row).Error; err != nil {
		return nil
	}
	var ext struct {
		StoredPath      string `json:"stored_path"`
		ParseStoredPath string `json:"parse_stored_path"`
	}
	if json.Unmarshal(row.Ext, &ext) != nil {
		return nil
	}
	path := strings.TrimSpace(ext.ParseStoredPath)
	if path == "" {
		path = strings.TrimSpace(ext.StoredPath)
	}
	serviceURL := strings.Replace(strings.TrimSpace(os.Getenv("LAZYMIND_OFFICE_CONVERT_URL")),
		"/v1/office/to-pdf", "/v1/pdf/reference-regions", 1)
	if serviceURL == "" || path == "" || !strings.EqualFold(filepath.Ext(path), ".pdf") {
		return nil
	}
	rawReferences := make([]string, len(references))
	for index := range references {
		rawReferences[index] = references[index].Raw
	}
	var response struct {
		References []referenceRegionResult `json:"references"`
	}
	if err := common.ApiPost(ctx, serviceURL, map[string]any{"source_path": path, "references": rawReferences}, nil, &response, 2*time.Minute); err != nil {
		return nil
	}
	result := make(map[int][]referenceRegion, len(response.References))
	for _, item := range response.References {
		if len(item.Regions) > 0 {
			result[item.Index] = item.Regions
		}
	}
	return result
}

func bibliographySection(text string) string {
	lines := strings.Split(text, "\n")
	start := -1
	heading := regexp.MustCompile(`(?i)^\s*(references|bibliography|参考文献)\s*[:：]?\s*$`)
	endHeading := regexp.MustCompile(`(?i)^\s*(appendix|acknowledg(e)?ments?|author biography|附录|致谢)\b`)
	for i, line := range lines {
		if start < 0 && heading.MatchString(line) {
			start = i + 1
			continue
		}
		if start >= 0 && endHeading.MatchString(line) {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start >= 0 {
		section := strings.Join(lines[start:], "\n")
		// Some readers serialize the final reference and the appendix heading
		// into the same root node/line. Stop at the inline heading instead of
		// absorbing the appendix into the final bibliography entry.
		inlineEnd := regexp.MustCompile(`(?i)(?:\s|^)(?:[A-Z]\s+)?(APPENDIX|ACKNOWLEDG(?:E)?MENTS?|附录|致谢)(?:\s|$)`)
		if location := inlineEnd.FindStringIndex(section); location != nil {
			section = section[:location[0]]
		}
		return section
	}
	return ""
}

type extractedReference struct {
	Key, Raw, DOI, Arxiv string
	Year                 int
}

func mergeReferenceFragments(items []extractedReference) []extractedReference {
	merged := make([]extractedReference, 0, len(items))
	for index := 0; index < len(items); index++ {
		item := items[index]
		if item.Year == 0 && strings.HasSuffix(strings.TrimSpace(item.Raw), ",") && index+1 < len(items) {
			item.Raw = strings.TrimSpace(item.Raw + " " + items[index+1].Raw)
			item.DOI, item.Arxiv, _ = extractStrongIDs(item.Raw)
			item.Year = extractYear(item.Raw)
			index++
		}
		merged = append(merged, item)
	}
	return merged
}

func splitReferences(section string) []extractedReference {
	section = strings.TrimSpace(section)
	if section == "" {
		return nil
	}
	// If the bibliography uses explicit numbers, a dash at the beginning of a
	// new reader block is only layout punctuation. Treating it as a new entry
	// splits references at column/page boundaries (for example, an author list
	// continued on the next page). Only fall back to bullets when no numbered
	// entries exist at all.
	locs := numberedReferenceStartPattern.FindAllStringSubmatchIndex(section, -1)
	// A lone bracket-like line can occur inside an author-year citation. It is
	// not enough to classify the whole bibliography as numbered.
	numbered := len(locs) >= 2
	if !numbered {
		locs = bulletReferenceStartPattern.FindAllStringSubmatchIndex(section, -1)
	}
	items := make([]extractedReference, 0, len(locs))
	if len(locs) == 0 {
		paragraphs := regexp.MustCompile(`\n\s*\n`).Split(section, -1)
		// Author-year bibliographies are commonly unnumbered. pdf.js preserves
		// visual line endings but not paragraph gaps, so use a conservative
		// author-name boundary when blank paragraphs are unavailable.
		if len(paragraphs) <= 1 {
			authorStart := regexp.MustCompile(`^[A-Z][A-Za-z'’.-]+,\s+(?:[A-Z](?:\.|,)|[A-Z][a-z]+)`)
			paragraphs = nil
			current := ""
			for _, line := range strings.Split(section, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if current != "" && authorStart.MatchString(line) {
					paragraphs = append(paragraphs, current)
					current = line
				} else {
					current = strings.TrimSpace(current + " " + line)
				}
			}
			if current != "" {
				paragraphs = append(paragraphs, current)
			}
		}
		for i, paragraph := range paragraphs {
			raw := strings.Join(strings.Fields(paragraph), " ")
			if len(raw) < 20 {
				continue
			}
			doi, arxiv, _ := extractStrongIDs(raw)
			items = append(items, extractedReference{Key: fmt.Sprint(i + 1), Raw: raw, DOI: doi, Arxiv: arxiv, Year: extractYear(raw)})
		}
		return mergeReferenceFragments(items)
	}
	for i, loc := range locs {
		end := len(section)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		raw := strings.Join(strings.Fields(section[loc[1]:end]), " ")
		if len(raw) < 10 {
			continue
		}
		key := ""
		if numbered {
			for _, pair := range [][2]int{{loc[2], loc[3]}, {loc[4], loc[5]}} {
				if pair[0] >= 0 {
					key = section[pair[0]:pair[1]]
					break
				}
			}
		}
		doi, arxiv, _ := extractStrongIDs(raw)
		items = append(items, extractedReference{Key: key, Raw: raw, DOI: doi, Arxiv: arxiv, Year: extractYear(raw)})
	}
	return mergeReferenceFragments(items)
}

func emptyJSON(value string) json.RawMessage { return json.RawMessage(value) }

func ListReferences(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	documentID := mux.Vars(r)["document_id"]
	datasetID, err := documentDataset(r.Context(), documentID)
	if err != nil {
		replyError(w, http.StatusNotFound, "DOCUMENT_NOT_FOUND", "document not found")
		return
	}
	service, _ := doc.NewDocumentService(doc.DocumentServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
	if _, err = service.GetDocumentMetadata(r.Context(), doc.DocumentGetRequest{UserID: userID, DatasetID: datasetID, DocumentID: documentID, Caller: caller(r)}); err != nil {
		replyError(w, http.StatusForbidden, "DOCUMENT_FORBIDDEN", "document is unavailable")
		return
	}
	var rows []orm.AcademicReference
	if err = store.DB().WithContext(r.Context()).Where("source_document_id = ?", documentID).Order("reference_key ASC, created_at ASC").Find(&rows).Error; err != nil {
		replyError(w, http.StatusInternalServerError, "REFERENCE_QUERY_FAILED", "query references failed")
		return
	}
	common.ReplyJSON(w, map[string]any{"document_id": documentID, "references": rows, "total": len(rows)})
}

func ExtractReferences(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	documentID := mux.Vars(r)["document_id"]
	datasetID, err := documentDataset(r.Context(), documentID)
	if err != nil {
		replyError(w, http.StatusNotFound, "DOCUMENT_NOT_FOUND", "document not found")
		return
	}
	rows, err := extractDocumentReferences(r.Context(), r, userID, datasetID, documentID, true)
	if err != nil {
		replyError(w, http.StatusUnprocessableEntity, "REFERENCE_EXTRACTION_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"document_id": documentID, "references": rows, "total": len(rows), "extractor_version": extractorVersion})
}

func extractDocumentReferences(ctx context.Context, r *http.Request, userID, datasetID, documentID string, force bool) ([]orm.AcademicReference, error) {
	service, err := doc.NewDocumentService(doc.DocumentServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
	if err != nil {
		return nil, err
	}
	if _, err = service.GetDocumentMetadata(ctx, doc.DocumentGetRequest{UserID: userID, DatasetID: datasetID, DocumentID: documentID, Caller: caller(r)}); err != nil {
		return nil, err
	}
	if !force {
		var existing []orm.AcademicReference
		if err := store.DB().WithContext(ctx).Where("source_document_id = ?", documentID).Order("reference_key ASC, created_at ASC").Find(&existing).Error; err != nil {
			return nil, err
		}
		if len(existing) > 0 && existing[0].ExtractorVersion == extractorVersion {
			return existing, nil
		}
	}
	text, layoutBlocks, err := documentTextWithLayout(ctx, r, userID, datasetID, documentID)
	if err != nil {
		return nil, fmt.Errorf("parsed text required: %w", err)
	}
	items := splitReferences(bibliographySection(text))
	if len(items) == 0 {
		return nil, errors.New("no reference entries were detected")
	}
	nativeRegions := nativeReferenceRegions(ctx, documentID, items)
	now := time.Now().UTC()
	var previous []orm.AcademicReference
	if err := store.DB().WithContext(ctx).Where("source_document_id = ? AND extractor_name = ?", documentID, "builtin_bibliography").Find(&previous).Error; err != nil {
		return nil, err
	}
	byDOI, byArxiv, byRaw := map[string]orm.AcademicReference{}, map[string]orm.AcademicReference{}, map[string]orm.AcademicReference{}
	for _, ref := range previous {
		if ref.DOINormalized != "" {
			byDOI[ref.DOINormalized] = ref
		}
		if ref.ArxivIDBase != "" {
			byArxiv[ref.ArxivIDBase] = ref
		}
		byRaw[NormalizeTitle(ref.RawText)] = ref
	}
	rows := make([]orm.AcademicReference, 0, len(items))
	usedPreviousIDs := map[string]bool{}
	for itemIndex, item := range items {
		page, bbox := referenceLayout(item.Raw, layoutBlocks)
		if regions := nativeRegions[itemIndex]; len(regions) > 0 {
			page = regions[0].Page
			bbox, _ = json.Marshal(map[string]any{"regions": regions})
		}
		old, matched := byRaw[NormalizeTitle(item.Raw)]
		if item.DOI != "" {
			if candidate, ok := byDOI[item.DOI]; ok {
				old, matched = candidate, true
			}
		}
		if item.Arxiv != "" {
			if candidate, ok := byArxiv[item.Arxiv]; ok {
				old, matched = candidate, true
			}
		}
		if matched && usedPreviousIDs[old.ID] {
			matched = false
			old = orm.AcademicReference{}
		}
		id, createdAt := uuid.NewString(), now
		if matched {
			id, createdAt = old.ID, old.CreatedAt
			usedPreviousIDs[old.ID] = true
		}
		rows = append(rows, orm.AcademicReference{ID: id, SourceDocumentID: documentID, SourceWorkID: old.SourceWorkID, ReferenceKey: item.Key,
			RawText: item.Raw, AuthorsJSON: emptyJSON("[]"), PublicationYear: item.Year, DOINormalized: item.DOI, ArxivIDBase: item.Arxiv,
			ResolvedWorkID: old.ResolvedWorkID, ResolutionStatus: firstNonEmpty(old.ResolutionStatus, "unresolved"), ResolutionMethod: old.ResolutionMethod,
			ResolutionConfidence: old.ResolutionConfidence, Page: page, BBoxJSON: bbox, SegmentIDsJSON: emptyJSON("[]"), ExtractorName: "builtin_bibliography",
			ExtractorVersion: extractorVersion, SourceFingerprint: old.SourceFingerprint, CreatedAt: createdAt, UpdatedAt: now})
	}
	err = store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("source_document_id = ? AND extractor_name = ?", documentID, "builtin_bibliography").Delete(&orm.AcademicReference{}).Error; err != nil {
			return err
		}
		return tx.Create(&rows).Error
	})
	return rows, err
}

type resolveRequest struct {
	ReferenceIDs []string    `json:"reference_ids"`
	Works        []WorkInput `json:"works"`
}

func findOrCreateWork(ctx context.Context, tx *gorm.DB, in WorkInput) (orm.AcademicWork, string, error) {
	if in.WorkID != "" {
		var row orm.AcademicWork
		err := tx.WithContext(ctx).Where("id = ?", in.WorkID).Take(&row).Error
		return row, "id", err
	}
	doi := NormalizeDOI(in.DOI)
	arxiv, _ := NormalizeArxivID(in.ArxivID)
	var row orm.AcademicWork
	if doi != "" && tx.WithContext(ctx).Where("doi_normalized = ?", doi).Take(&row).Error == nil {
		updates := map[string]any{"updated_at": time.Now().UTC()}
		if arxiv != "" && row.ArxivIDBase == "" {
			updates["arxiv_id_base"] = arxiv
			row.ArxivIDBase = arxiv
		}
		var existingProvenance map[string]any
		_ = json.Unmarshal(row.ProvenanceJSON, &existingProvenance)
		existingPDF, existingHasPDF := existingProvenance["pdf_url"].(string)
		existingHasPDF = existingHasPDF && strings.TrimSpace(existingPDF) != ""
		incomingPDF, incomingHasPDF := in.Provenance["pdf_url"].(string)
		incomingHasPDF = incomingHasPDF && strings.TrimSpace(incomingPDF) != ""
		if len(in.Provenance) > 0 && (!existingHasPDF || incomingHasPDF) {
			provenance, _ := json.Marshal(in.Provenance)
			updates["metadata_provenance_json"] = provenance
			row.ProvenanceJSON = provenance
		}
		_ = tx.WithContext(ctx).Model(&row).Updates(updates).Error
		return row, "doi", nil
	}
	if arxiv != "" && tx.WithContext(ctx).Where("arxiv_id_base = ?", arxiv).Take(&row).Error == nil {
		if doi != "" && row.DOINormalized == "" {
			_ = tx.WithContext(ctx).Model(&row).Updates(map[string]any{"doi_normalized": doi, "updated_at": time.Now().UTC()}).Error
			row.DOINormalized = doi
		}
		return row, "arxiv", nil
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = firstNonEmpty(doi, arxiv)
	}
	if title == "" {
		return row, "", errors.New("work requires DOI, arXiv ID, or title")
	}
	normalizedTitle := NormalizeTitle(title)
	firstAuthor := ""
	if len(in.Authors) > 0 {
		firstAuthor = normalizeAuthor(in.Authors[0])
	}
	if doi == "" && arxiv == "" && normalizedTitle != "" {
		q := tx.WithContext(ctx).Where("normalized_title = ?", normalizedTitle)
		if firstAuthor != "" {
			q = q.Where("first_author_normalized = ?", firstAuthor)
		}
		if in.Year > 0 {
			q = q.Where("publication_year = ?", in.Year)
		}
		q = q.Order("(SELECT COUNT(*) FROM academic_work_documents awd WHERE awd.academic_work_id = academic_works.id) DESC")
		if q.Take(&row).Error == nil {
			return row, map[bool]string{true: "title_author_year", false: "title_year"}[firstAuthor != ""], nil
		}
	}
	authors, _ := json.Marshal(in.Authors)
	external, _ := json.Marshal(map[string]string{in.Provider: in.ProviderWorkID})
	provenance, _ := json.Marshal(in.Provenance)
	now := time.Now().UTC()
	row = orm.AcademicWork{ID: uuid.NewString(), CanonicalTitle: title, NormalizedTitle: normalizedTitle, AuthorsJSON: authors,
		FirstAuthorNormalized: firstAuthor, PublicationYear: in.Year, Venue: in.Venue, Abstract: in.Abstract, DOINormalized: doi,
		ArxivIDBase: arxiv, ExternalIDsJSON: external, ProvenanceJSON: provenance, ResolutionConfidence: 1, CreatedAt: now, UpdatedAt: now}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return row, "", err
	}
	if doi != "" {
		_ = tx.WithContext(ctx).Where("doi_normalized = ?", doi).Take(&row).Error
	}
	if arxiv != "" {
		_ = tx.WithContext(ctx).Where("arxiv_id_base = ?", arxiv).Take(&row).Error
	}
	return row, firstNonEmpty(map[bool]string{true: "doi", false: ""}[doi != ""], map[bool]string{true: "arxiv", false: ""}[arxiv != ""], "metadata"), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func resolveReference(ctx context.Context, tx *gorm.DB, ref *orm.AcademicReference) (orm.AcademicWork, string, string, error) {
	return resolveReferenceWithExternal(ctx, tx, ref, true)
}

func resolveReferenceWithExternal(ctx context.Context, tx *gorm.DB, ref *orm.AcademicReference, allowExternal bool) (orm.AcademicWork, string, string, error) {
	if !allowExternal && ref.ResolvedWorkID != "" {
		var existing orm.AcademicWork
		if err := tx.WithContext(ctx).Where("id = ?", ref.ResolvedWorkID).Take(&existing).Error; err == nil {
			return existing, "id", ref.ResolutionStatus, nil
		}
	}
	input := WorkInput{Title: ref.Title, Year: ref.PublicationYear, DOI: ref.DOINormalized, ArxivID: ref.ArxivIDBase}
	if input.Title == "" {
		input.Title = ref.RawText
	}
	if allowExternal && input.ArxivID == "" {
		openAlex, openAlexOK := resolveWithOpenAlex(ctx, ref.RawText)
		openAlexHasFulltext := openAlex.ArxivID != ""
		if pdfURL, _ := openAlex.Provenance["pdf_url"].(string); strings.TrimSpace(pdfURL) != "" {
			openAlexHasFulltext = true
		}
		// Prefer an arXiv copy over publisher-hosted PDFs. Publisher URLs
		// frequently return 403 to server-side clients despite being marked OA.
		candidate, arxivOK := resolveWithArxiv(ctx, ref.RawText)
		if openAlexOK && openAlex.ArxivID != "" {
			if input.DOI != "" {
				openAlex.DOI = input.DOI
			}
			input = openAlex
		} else if arxivOK {
			candidate.DOI = input.DOI
			input = candidate
		} else if openAlexOK && openAlexHasFulltext {
			if input.DOI != "" {
				openAlex.DOI = input.DOI
			}
			input = openAlex
		} else if openAlexOK {
			if input.DOI != "" {
				openAlex.DOI = input.DOI
			}
			input = openAlex
		}
	}
	work, method, err := findOrCreateWork(ctx, tx, input)
	if err != nil {
		return work, "", "", err
	}
	status, confidence := "high_confidence", 0.9
	if method == "doi" || method == "arxiv" {
		status, confidence = "exact", 1
	}
	if err := tx.Model(ref).Updates(map[string]any{"resolved_work_id": work.ID, "arxiv_id_base": work.ArxivIDBase, "resolution_status": status, "resolution_method": method, "resolution_confidence": confidence, "updated_at": time.Now().UTC()}).Error; err != nil {
		return work, "", "", err
	}
	ref.ResolvedWorkID, ref.ResolutionStatus = work.ID, status
	return work, method, status, nil
}

func ResolveReferences(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}
	var req resolveRequest
	if !parseJSON(r, &req) || (len(req.ReferenceIDs) == 0 && len(req.Works) == 0) {
		replyError(w, http.StatusBadRequest, "INVALID_REQUEST", "reference_ids or works is required")
		return
	}
	resolved := make([]map[string]any, 0, len(req.ReferenceIDs)+len(req.Works))
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		for _, input := range req.Works {
			work, method, err := findOrCreateWork(r.Context(), tx, input)
			if err != nil {
				return err
			}
			resolved = append(resolved, map[string]any{"work": work, "method": method, "status": "exact"})
		}
		for _, id := range req.ReferenceIDs {
			var ref orm.AcademicReference
			if err := tx.Where("id = ?", id).Take(&ref).Error; err != nil {
				return err
			}
			work, method, status, err := resolveReference(r.Context(), tx, &ref)
			if err != nil {
				return err
			}
			resolved = append(resolved, map[string]any{"reference_id": id, "work": work, "method": method, "status": status})
		}
		return nil
	})
	if err != nil {
		replyError(w, http.StatusUnprocessableEntity, "RESOLUTION_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"items": resolved})
}

type presenceRequest struct {
	WorkIDs          []string    `json:"work_ids"`
	Works            []WorkInput `json:"works"`
	CurrentDatasetID string      `json:"current_dataset_id,omitempty"`
}

func presenceForWorks(ctx context.Context, r *http.Request, userID string, workIDs []string, currentDatasetID string) ([]PresenceResult, error) {
	visible, err := accessibleDatasets(ctx, r, userID)
	if err != nil {
		return nil, err
	}
	results := make([]PresenceResult, 0, len(workIDs))
	for _, workID := range workIDs {
		result := PresenceResult{WorkID: workID, Status: "absent", CoverageComplete: true, CurrentDataset: []PresenceDocument{}, OtherDatasets: []PresenceDocument{}}
		var links []orm.AcademicWorkDocument
		if err := store.DB().WithContext(ctx).Where("academic_work_id = ?", workID).Find(&links).Error; err != nil {
			return nil, err
		}
		for _, link := range links {
			dataset, ok := visible[link.DatasetID]
			if !ok {
				continue
			}
			var document orm.Document
			if store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", link.DocumentID, link.DatasetID).Take(&document).Error != nil {
				continue
			}
			item := PresenceDocument{DatasetID: link.DatasetID, DatasetName: dataset.DisplayName, DocumentID: link.DocumentID, DocumentName: document.DisplayName, SourceVersion: link.SourceVersion, ContentSHA256: link.ContentSHA256}
			if link.DatasetID == currentDatasetID {
				result.CurrentDataset = append(result.CurrentDataset, item)
			} else {
				result.OtherDatasets = append(result.OtherDatasets, item)
			}
		}
		var importing int64
		_ = store.DB().WithContext(ctx).Table("paper_import_items pi").Joins("JOIN paper_import_batches pb ON pb.id = pi.batch_id").Where("pi.academic_work_id = ? AND pb.created_by = ? AND pi.status IN ?", workID, userID, []string{"pending", "running"}).Count(&importing).Error
		result.Importing = importing > 0
		if len(result.CurrentDataset) > 0 {
			result.Status = "current_dataset"
		} else if len(result.OtherDatasets) > 0 {
			result.Status = "other_dataset"
		} else if result.Importing {
			result.Status = "importing"
		}
		results = append(results, result)
	}
	return results, nil
}

func CheckPresence(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req presenceRequest
	if !parseJSON(r, &req) {
		replyError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request")
		return
	}
	workIDs := append([]string(nil), req.WorkIDs...)
	if len(req.Works) > 0 {
		err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
			for _, input := range req.Works {
				work, _, err := findOrCreateWork(r.Context(), tx, input)
				if err != nil {
					return err
				}
				workIDs = append(workIDs, work.ID)
			}
			return nil
		})
		if err != nil {
			replyError(w, http.StatusUnprocessableEntity, "RESOLUTION_FAILED", err.Error())
			return
		}
	}
	if len(workIDs) == 0 || len(workIDs) > 500 {
		replyError(w, http.StatusBadRequest, "INVALID_REQUEST", "1 to 500 works are required")
		return
	}
	results, err := presenceForWorks(r.Context(), r, userID, workIDs, req.CurrentDatasetID)
	if err != nil {
		replyError(w, http.StatusInternalServerError, "PRESENCE_QUERY_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"items": results})
}

func candidatesForWork(work orm.AcademicWork) []FulltextCandidate {
	if work.ArxivIDBase != "" {
		return []FulltextCandidate{{URL: "https://arxiv.org/pdf/" + work.ArxivIDBase + ".pdf", SourceType: "official_pdf", SourceProvider: "arxiv", ExpectedMIME: "application/pdf"}}
	}
	var provenance map[string]any
	_ = json.Unmarshal(work.ProvenanceJSON, &provenance)
	pdfURL, _ := provenance["pdf_url"].(string)
	if strings.TrimSpace(pdfURL) == "" {
		return []FulltextCandidate{}
	}
	license, _ := provenance["pdf_license"].(string)
	version, _ := provenance["pdf_version"].(string)
	return []FulltextCandidate{{URL: pdfURL, SourceType: "open_access_pdf", SourceProvider: "openalex", License: license, Version: version, ExpectedMIME: "application/pdf"}}
}

type previewRequest struct {
	ReferenceIDs, SourceDocumentIDs, WorkIDs []string
	TargetDatasetID, TargetPID               string
	Works                                    []WorkInput `json:"works"`
}

func PreviewImport(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		ReferenceIDs      []string    `json:"reference_ids"`
		SourceDocumentIDs []string    `json:"source_document_ids"`
		WorkIDs           []string    `json:"work_ids"`
		Works             []WorkInput `json:"works"`
		TargetDatasetID   string      `json:"target_dataset_id"`
		TargetPID         string      `json:"target_pid,omitempty"`
		ResolveExternal   *bool       `json:"resolve_external,omitempty"`
	}
	if !parseJSON(r, &req) || req.TargetDatasetID == "" {
		replyError(w, http.StatusBadRequest, "INVALID_REQUEST", "target_dataset_id is required")
		return
	}
	if !canWriteDataset(r.Context(), req.TargetDatasetID, userID) {
		replyError(w, http.StatusForbidden, "TARGET_DATASET_FORBIDDEN", "target knowledge base is not writable")
		return
	}
	referenceMap := map[string][]string{}
	resolveExternal := req.ResolveExternal == nil || *req.ResolveExternal
	workIDs := append([]string(nil), req.WorkIDs...)
	for _, documentID := range req.SourceDocumentIDs {
		datasetID, err := documentDataset(r.Context(), documentID)
		if err != nil {
			replyError(w, http.StatusNotFound, "SOURCE_DOCUMENT_NOT_FOUND", "one or more source documents were not found")
			return
		}
		if _, err = extractDocumentReferences(r.Context(), r, userID, datasetID, documentID, false); err != nil {
			replyError(w, http.StatusUnprocessableEntity, "SOURCE_REFERENCE_EXTRACTION_FAILED", fmt.Sprintf("%s: %v", documentID, err))
			return
		}
	}
	query := store.DB().WithContext(r.Context()).Model(&orm.AcademicReference{})
	if len(req.ReferenceIDs) > 0 {
		query = query.Where("id IN ?", req.ReferenceIDs)
	} else if len(req.SourceDocumentIDs) > 0 {
		query = query.Where("source_document_id IN ?", req.SourceDocumentIDs)
	}
	if len(req.ReferenceIDs) > 0 || len(req.SourceDocumentIDs) > 0 {
		var refs []orm.AcademicReference
		if err := query.Find(&refs).Error; err != nil {
			replyError(w, 500, "REFERENCE_QUERY_FAILED", err.Error())
			return
		}
		for index := range refs {
			ref := &refs[index]
			if ref.ResolvedWorkID == "" || (resolveExternal && ref.ArxivIDBase == "") {
				err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
					_, _, _, resolveErr := resolveReferenceWithExternal(r.Context(), tx, ref, resolveExternal)
					return resolveErr
				})
				if err != nil {
					continue
				}
			}
			if ref.ResolvedWorkID != "" {
				referenceMap[ref.ResolvedWorkID] = append(referenceMap[ref.ResolvedWorkID], ref.ID)
				workIDs = append(workIDs, ref.ResolvedWorkID)
			}
		}
	}
	if len(req.Works) > 0 {
		_ = store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
			for _, input := range req.Works {
				work, _, err := findOrCreateWork(r.Context(), tx, input)
				if err != nil {
					return err
				}
				workIDs = append(workIDs, work.ID)
			}
			return nil
		})
	}
	seen := map[string]bool{}
	unique := workIDs[:0]
	for _, id := range workIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	workIDs = unique
	if len(workIDs) == 0 {
		replyError(w, http.StatusUnprocessableEntity, "UNRESOLVED_REFERENCES", "resolve references before creating an import preview")
		return
	}
	presence, err := presenceForWorks(r.Context(), r, userID, workIDs, req.TargetDatasetID)
	if err != nil {
		replyError(w, 500, "PRESENCE_QUERY_FAILED", err.Error())
		return
	}
	presenceByID := map[string]PresenceResult{}
	for _, item := range presence {
		presenceByID[item.WorkID] = item
	}
	var works []orm.AcademicWork
	if err := store.DB().WithContext(r.Context()).Where("id IN ?", workIDs).Find(&works).Error; err != nil {
		replyError(w, 500, "WORK_QUERY_FAILED", err.Error())
		return
	}
	items := make([]PreviewItem, 0, len(works))
	for _, work := range works {
		p := presenceByID[work.ID]
		candidates := candidatesForWork(work)
		disposition := "downloadable"
		if len(p.CurrentDataset) > 0 {
			disposition = "already_present"
		} else if p.Importing {
			disposition = "importing"
		} else if len(candidates) == 0 {
			disposition = "metadata_only"
		}
		items = append(items, PreviewItem{WorkID: work.ID, ReferenceIDs: referenceMap[work.ID], Title: work.CanonicalTitle, DOI: work.DOINormalized, ArxivID: work.ArxivIDBase, Presence: p, Candidates: candidates, Disposition: disposition})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Title < items[j].Title })
	common.ReplyJSON(w, map[string]any{"preview_token": uuid.NewString(), "target_dataset_id": req.TargetDatasetID, "items": items, "total": len(items)})
}
