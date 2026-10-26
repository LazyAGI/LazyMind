// Package paperlibrary resolves the dataset's editable paper graph against its
// originals. It does not depend on HTTP handlers or document-processing services.
package paperlibrary

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

type Graph map[string]any
type Entity = map[string]any

type View struct {
	Data               json.RawMessage `json:"data"`
	PaperCount         int64           `json:"paper_count"`
	PDFAttachmentCount int64           `json:"pdf_attachment_count"`
	PaperImportance    map[string]bool `json:"paper_importance"`
	Active             bool            `json:"active"`
}

func Empty() Graph {
	graph := Graph{"schemaVersion": 3}
	for _, key := range []string{"folders", "collections", "papers", "attachments", "notes", "annotations", "relations", "mergeSnapshots"} {
		graph[key] = []any{}
	}
	return graph
}

func Decode(raw json.RawMessage) (Graph, error) {
	graph := Empty()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &graph); err != nil {
			return nil, err
		}
	}
	return graph, nil
}

func Objects(graph Graph, key string) []Entity {
	var result []Entity
	for _, value := range array(graph[key]) {
		if item, ok := value.(map[string]any); ok {
			result = append(result, item)
		}
	}
	return result
}

func SetObjects(graph Graph, key string, objects []Entity) {
	values := make([]any, 0, len(objects))
	for _, object := range objects {
		values = append(values, object)
	}
	graph[key] = values
}

func Text(item Entity, key string) string {
	value, _ := item[key].(string)
	return strings.TrimSpace(value)
}

func StringList(value any) []string {
	result := []string{}
	if values, ok := value.([]string); ok {
		return append(result, values...)
	}
	for _, value := range array(value) {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func array(value any) []any { values, _ := value.([]any); return values }

var arxivVersion = regexp.MustCompile(`(?i)v[1-9][0-9]*$`)

func ArxivBase(value string) string {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".pdf")
	for _, prefix := range []string{"https://arxiv.org/abs/", "http://arxiv.org/abs/", "https://arxiv.org/pdf/", "http://arxiv.org/pdf/", "arxiv:"} {
		value = strings.TrimPrefix(value, prefix)
	}
	return arxivVersion.ReplaceAllString(value, "")
}

func DOI(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"https://doi.org/", "http://doi.org/", "https://dx.doi.org/", "http://dx.doi.org/", "doi:"} {
		value = strings.TrimPrefix(value, prefix)
	}
	return strings.TrimRight(value, ".,;)]}")
}

// Identifier accepts the v3 metadata location as well as legacy top-level fields.
func Identifier(paper Entity, name string) string {
	if value := Text(paper, name); value != "" {
		return value
	}
	metadata, _ := paper["metadata"].(map[string]any)
	identifiers, _ := metadata["identifiers"].(map[string]any)
	return Text(identifiers, name)
}

// MatchIdentity never treats a title or a filename as proof of identity. A
// shared arXiv base identifies a work independently of publication DOI/version.
func MatchIdentity(left, right Entity) string {
	if id := Text(left, "academicWorkId"); id != "" && id == Text(right, "academicWorkId") {
		return "match"
	}
	a, b := ArxivBase(Identifier(left, "arxiv")), ArxivBase(Identifier(right, "arxiv"))
	d, e := DOI(Identifier(left, "doi")), DOI(Identifier(right, "doi"))
	if a != "" && b != "" {
		if a == b {
			return "match"
		}
		if d != "" && d == e {
			return "uncertain"
		}
		return "conflict"
	}
	if d != "" && e != "" {
		if d == e {
			return "match"
		}
		return "conflict"
	}
	return "uncertain"
}

func WorkEntity(work orm.AcademicWork) Entity {
	var authors []string
	_ = json.Unmarshal(work.AuthorsJSON, &authors)
	return Entity{"academicWorkId": work.ID, "title": work.CanonicalTitle, "authors": strings.Join(authors, "、"), "year": work.PublicationYear, "venue": work.Venue, "doi": work.DOINormalized, "arxiv": work.ArxivIDBase, "abstract": work.Abstract}
}

// LoadMany uses bounded, set-based queries for both the library and catalog.
// Graphs remain maps so unknown fields and archived annotation references survive.
func LoadMany(ctx context.Context, db *gorm.DB, datasetIDs []string, userID string) (map[string]View, error) {
	result := map[string]View{}
	if len(datasetIDs) == 0 {
		return result, nil
	}
	db = db.WithContext(ctx)
	var libraries []orm.PaperLibrary
	var documents []orm.Document
	var states []orm.AcademicDocumentState
	var links []orm.AcademicWorkDocument
	var processing []orm.DocumentProcessingState
	var origins []struct {
		DocumentID, Stage, EntryType         string
		ReferenceIDsJSON, PolicySnapshotJSON json.RawMessage
	}
	for _, query := range []*gorm.DB{
		db.Where("dataset_id IN ?", datasetIDs).Find(&libraries),
		db.Where("dataset_id IN ? AND deleted_at IS NULL", datasetIDs).Order("created_at, id").Find(&documents),
		db.Where("dataset_id IN ?", datasetIDs).Find(&states),
		db.Where("dataset_id IN ?", datasetIDs).Find(&links),
		db.Where("dataset_id IN ?", datasetIDs).Find(&processing),
		db.Table("paper_import_items AS i").Select("i.document_id, i.stage, i.reference_ids_json, b.entry_type, b.policy_snapshot_json").Joins("JOIN paper_import_batches AS b ON b.id=i.batch_id").Where("b.target_dataset_id IN ? AND i.document_id<>?", datasetIDs, "").Order("i.created_at, i.id").Scan(&origins),
	} {
		if query.Error != nil {
			return nil, query.Error
		}
	}
	graphs := map[string]Graph{}
	for _, id := range datasetIDs {
		graphs[id] = Empty()
	}
	for _, library := range libraries {
		graph, err := Decode(library.DataJSON)
		if err != nil {
			return nil, fmt.Errorf("read paper graph: %w", err)
		}
		graphs[library.DatasetID] = graph
	}
	workIDs := []string{}
	for _, state := range states {
		if state.PaperID != "" {
			workIDs = append(workIDs, state.PaperID)
		}
	}
	for _, graph := range graphs {
		for _, paper := range Objects(graph, "papers") {
			if id := Text(paper, "academicWorkId"); id != "" {
				workIDs = append(workIDs, id)
			}
		}
	}
	works := map[string]orm.AcademicWork{}
	marks := map[string]bool{}
	if len(workIDs) > 0 {
		var rows []orm.AcademicWork
		if err := db.Where("id IN ?", workIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, work := range rows {
			works[work.ID] = work
		}
		if userID != "" {
			var rows []orm.AcademicUserPaperState
			if err := db.Where("user_id=? AND paper_id IN ?", userID, workIDs).Find(&rows).Error; err != nil {
				return nil, err
			}
			for _, mark := range rows {
				marks[mark.PaperID] = mark.IsImportant
			}
		}
	}
	stateByDocument := map[string]orm.AcademicDocumentState{}
	linkByDocument := map[string]orm.AcademicWorkDocument{}
	processingByDocument := map[string]orm.DocumentProcessingState{}
	pending := map[string]bool{}
	provenance := map[string]Entity{}
	for _, state := range states {
		stateByDocument[state.DocumentID] = state
	}
	for _, link := range links {
		linkByDocument[link.DocumentID] = link
	}
	for _, row := range processing {
		processingByDocument[row.DocumentID] = row
	}
	for _, origin := range origins {
		if origin.EntryType == "pdf_upload" && origin.Stage == "awaiting_association" {
			pending[origin.DocumentID] = true
		}
		if provenance[origin.DocumentID] != nil {
			continue
		}
		method := map[string]string{"paper_link": "link", "pdf_upload": "upload", "paper_expansion": "expansion"}[origin.EntryType]
		var references []string
		_ = json.Unmarshal(origin.ReferenceIDsJSON, &references)
		if method == "" && len(references) > 0 {
			method = "references"
		}
		if method == "" {
			continue
		}
		value := Entity{"importMethod": method}
		if method == "expansion" {
			var policy struct {
				ExpansionID      string `json:"expansion_id"`
				ExpansionVersion int64  `json:"expansion_version"`
			}
			if json.Unmarshal(origin.PolicySnapshotJSON, &policy) == nil {
				value["expansionId"], value["expansionVersion"] = policy.ExpansionID, policy.ExpansionVersion
			}
		}
		provenance[origin.DocumentID] = value
	}
	byDataset := map[string][]orm.Document{}
	for _, document := range documents {
		byDataset[document.DatasetID] = append(byDataset[document.DatasetID], document)
	}
	for id, graph := range graphs {
		view, err := resolve(graph, byDataset[id], stateByDocument, linkByDocument, processingByDocument, works, marks, pending, provenance)
		if err != nil {
			return nil, err
		}
		result[id] = view
	}
	return result, nil
}

func resolve(graph Graph, documents []orm.Document, states map[string]orm.AcademicDocumentState, links map[string]orm.AcademicWorkDocument, processing map[string]orm.DocumentProcessingState, works map[string]orm.AcademicWork, marks map[string]bool, pending map[string]bool, provenance map[string]Entity) (View, error) {
	papers, attachments, folders := Objects(graph, "papers"), Objects(graph, "attachments"), Objects(graph, "folders")
	hidden, hiddenWorks := map[string]bool{}, map[string]bool{}
	for _, id := range StringList(graph["hiddenDocumentIds"]) {
		hidden[id] = true
	}
	for _, id := range StringList(graph["hiddenArxivWorkIds"]) {
		hiddenWorks[id] = true
	}
	documentsByID := map[string]orm.Document{}
	folderIDs := map[string]bool{}
	folderByID := map[string]Entity{}
	for _, folder := range folders {
		folderIDs[Text(folder, "id")] = true
		folderByID[Text(folder, "id")] = folder
	}
	for _, document := range documents {
		documentsByID[document.ID] = document
	}
	hiddenFolders := map[string]bool{}
	for _, id := range StringList(graph["hiddenFolderIds"]) {
		hiddenFolders[id] = true
	}
	for _, document := range documents {
		if !strings.EqualFold(document.DocumentType, "FOLDER") {
			continue
		}
		id := "document-folder-" + document.ID
		if hiddenFolders[id] {
			continue
		}
		if !folderIDs[id] {
			parent := any(nil)
			if ancestor, exists := documentsByID[document.PID]; exists && strings.EqualFold(ancestor.DocumentType, "FOLDER") && !hiddenFolders["document-folder-"+ancestor.ID] {
				parent = "document-folder-" + document.PID
			}
			folders = append(folders, Entity{"id": id, "name": document.DisplayName, "parentId": parent})
			folderIDs[id] = true
		} else if _, declared := folderByID[id]["parentId"]; !declared {
			parent := any(nil)
			if ancestor, exists := documentsByID[document.PID]; exists && strings.EqualFold(ancestor.DocumentType, "FOLDER") && !hiddenFolders["document-folder-"+ancestor.ID] {
				parent = "document-folder-" + document.PID
			}
			folderByID[id]["parentId"] = parent
			folderByID[id]["name"] = document.DisplayName
		}
	}
	byID, attachedByDocument := map[string]Entity{}, map[string]Entity{}
	for _, paper := range papers {
		byID[Text(paper, "id")] = paper
	}
	for _, attachment := range attachments {
		if id := Text(attachment, "contentRef"); id != "" {
			attachedByDocument[id] = attachment
		}
	}
	active := false
	for _, document := range documents {
		if !strings.EqualFold(filepath.Ext(document.DisplayName), ".pdf") || hidden[document.ID] || pending[document.ID] {
			continue
		}
		state, link := states[document.ID], links[document.ID]
		work, hasWork := works[state.PaperID]
		if hasWork && hiddenWorks[work.ID] {
			continue
		}
		attachment := attachedByDocument[document.ID]
		var paper Entity
		if attachment != nil {
			paper = byID[Text(attachment, "paperId")]
		}
		identity := Entity{}
		if hasWork {
			identity = WorkEntity(work)
		}
		if paper == nil && hasWork {
			var candidates []Entity
			for _, existing := range papers {
				if MatchIdentity(existing, identity) == "match" {
					candidates = append(candidates, existing)
				}
			}
			if len(candidates) == 1 {
				paper = candidates[0]
			}
		}
		if paper == nil {
			paper = Entity{"id": "document-" + document.ID, "title": strings.TrimSuffix(document.DisplayName, filepath.Ext(document.DisplayName)), "authors": "", "venue": "", "year": 0, "folderId": nil, "status": "unread", "important": false, "tags": []any{}, "source": "local", "parse": "parsing", "metadata": Entity{"identifiers": Entity{}, "confidence": Entity{}}, "attachmentIds": []any{}, "noteIds": []any{}, "annotationIds": []any{}, "collectionIds": []any{}, "relatedPaperIds": []any{}}
			if link.SourceProvider == "arxiv" {
				paper["source"] = "arxiv"
			} else if link.SourceProvider != "" && link.SourceProvider != "manual" && link.SourceProvider != "upload" {
				paper["source"] = "web"
			}
			if origin := provenance[document.ID]; origin != nil {
				for key, value := range origin {
					paper[key] = value
				}
			}
			if hasWork {
				for key, value := range identity {
					paper[key] = value
				}
				var identified Entity
				if json.Unmarshal(state.MetadataJSON, &identified) == nil {
					for _, key := range []string{"title", "venue", "abstract"} {
						if value := Text(identified, key); value != "" {
							paper[key] = value
						}
					}
					if authors := StringList(identified["authors"]); len(authors) > 0 {
						paper["authors"] = strings.Join(authors, "、")
					}
				}
			} else {
				paper["incomplete"] = true
			}
			if parent := "document-folder-" + document.PID; document.PID != "" && folderIDs[parent] {
				paper["folderId"] = parent
			}
			var tags []string
			if json.Unmarshal(document.Tags, &tags) == nil && tags != nil {
				paper["tags"] = tags
			}
			papers = append(papers, paper)
			byID[Text(paper, "id")] = paper
		} else if hasWork {
			switch MatchIdentity(paper, identity) {
			case "conflict":
				paper["associationConflict"] = "附件识别结果与论文不一致，请核对附件归属"
			case "match", "uncertain":
				if Text(paper, "academicWorkId") == "" {
					paper["academicWorkId"] = work.ID
				}
			}
		}
		if attachment == nil {
			attachment = Entity{"id": "document-attachment-" + document.ID, "paperId": Text(paper, "id"), "name": document.DisplayName, "kind": "pdf", "addedAt": document.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), "contentRef": document.ID, "source": "local"}
			if origin := provenance[document.ID]; origin != nil {
				for key, value := range origin {
					attachment[key] = value
				}
			}
			attachments = append(attachments, attachment)
			attachedByDocument[document.ID] = attachment
		}
		attachment["available"] = true
		if link.SourceProvider != "" {
			attachment["sourceProvider"] = link.SourceProvider
			if link.SourceVersion != "" {
				attachment["sourceVersion"] = link.SourceVersion
			}
			if link.SourceLocator != "" {
				attachment["sourceUrl"] = link.SourceLocator
			}
			if link.ContentSHA256 != "" {
				attachment["contentHash"] = link.ContentSHA256
			}
		}
		if link.SourceProvider == "arxiv" {
			attachment["source"] = "arxiv"
			if Text(attachment, "importMethod") == "" {
				attachment["importMethod"] = "link"
			}
		} else if link.SourceProvider == "upload" && Text(attachment, "importMethod") == "" {
			attachment["importMethod"] = "upload"
		} else if link.SourceProvider != "" && link.SourceProvider != "manual" && link.SourceProvider != "upload" {
			attachment["source"] = "web"
		}
		ids := StringList(paper["attachmentIds"])
		found := false
		for _, id := range ids {
			if id == Text(attachment, "id") {
				found = true
			}
		}
		if !found {
			ids = append(ids, Text(attachment, "id"))
			paper["attachmentIds"] = ids
		}
		if state.Status == "pending" || state.Status == "processing" {
			active = true
		}
	}
	importance, pdfDocuments := map[string]bool{}, map[string]bool{}
	for _, paper := range papers {
		ids := map[string]bool{}
		for _, id := range StringList(paper["attachmentIds"]) {
			ids[id] = true
		}
		var firstPDF, primary Entity
		for _, attachment := range attachments {
			if !ids[Text(attachment, "id")] || Text(attachment, "paperId") != Text(paper, "id") {
				continue
			}
			documentID := Text(attachment, "contentRef")
			if documentID != "" {
				_, exists := documentsByID[documentID]
				attachment["available"] = exists && !hidden[documentID] && !pending[documentID]
				if attachment["available"] != true {
					continue
				}
			}
			if Text(attachment, "kind") != "pdf" || documentID == "" {
				continue
			}
			if documentID != "" {
				pdfDocuments[documentID] = true
			}
			if firstPDF == nil {
				firstPDF = attachment
			}
			if attachment["isPrimary"] == true {
				primary = attachment
			}
		}
		if primary == nil && firstPDF != nil {
			firstPDF["isPrimary"] = true
			primary = firstPDF
		}
		if primary != nil {
			if status, ok := processing[Text(primary, "contentRef")]; ok {
				switch status.ParseStatus {
				case "succeeded", "success", "ready":
					paper["parse"] = "ready"
					delete(paper, "processingError")
				case "failed":
					paper["parse"] = "failed"
					paper["processingError"] = status.ParseErrorMessage
				default:
					paper["parse"] = "parsing"
					active = true
				}
			}
		}
		importance[Text(paper, "id")] = marks[Text(paper, "academicWorkId")]
		paper["important"] = false
	}
	SetObjects(graph, "folders", folders)
	SetObjects(graph, "papers", papers)
	SetObjects(graph, "attachments", attachments)
	raw, err := json.Marshal(graph)
	return View{Data: raw, PaperCount: int64(len(papers)), PDFAttachmentCount: int64(len(pdfDocuments)), PaperImportance: importance, Active: active}, err
}
