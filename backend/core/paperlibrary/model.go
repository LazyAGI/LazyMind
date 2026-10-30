package paperlibrary

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"strings"
	"time"
	"unicode"
)

type Identifiers struct {
	DOI   string `json:"doi,omitempty"`
	Arxiv string `json:"arxiv,omitempty"`
}
type Metadata struct {
	Title         string      `json:"title"`
	Authors       []string    `json:"authors"`
	Venue         string      `json:"venue"`
	Year          int         `json:"year"`
	Abstract      string      `json:"abstract,omitempty"`
	Identifiers   Identifiers `json:"identifiers"`
	CitationCount *int64      `json:"citationCount,omitempty"`
}
type Paper struct {
	ID                  string   `json:"id"`
	AcademicWorkID      string   `json:"academicWorkId"`
	Revision            int64    `json:"revision"`
	Metadata            Metadata `json:"metadata"`
	FolderID            *string  `json:"folderId"`
	Status              string   `json:"status"`
	Tags                []string `json:"tags"`
	Important           bool     `json:"important"`
	Source              string   `json:"source"`
	ImportMethod        string   `json:"importMethod,omitempty"`
	SourceURL           string   `json:"sourceUrl,omitempty"`
	ArxivVersion        string   `json:"arxivVersion,omitempty"`
	Parse               string   `json:"parse"`
	ProcessingError     string   `json:"processingError,omitempty"`
	Incomplete          bool     `json:"incomplete,omitempty"`
	AssociationConflict string   `json:"associationConflict,omitempty"`
	AttachmentIDs       []string `json:"attachmentIds"`
	NoteIDs             []string `json:"noteIds"`
}
type Folder struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	ParentID *string `json:"parentId"`
	Revision int64   `json:"revision"`
}

// AcademicState is the document identity needed by library task controls.
type AcademicState struct {
	DocumentID string `json:"document_id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	PaperID    string `json:"paper_id"`
	Revision   int64  `json:"revision"`
	Manual     bool   `json:"manual"`
	Error      string `json:"error,omitempty"`
}
type Attachment struct {
	AcademicState    *AcademicState `json:"academicState,omitempty"`
	ID               string         `json:"id"`
	PaperID          string         `json:"paperId"`
	Name             string         `json:"name"`
	Kind             string         `json:"kind"`
	ContentRef       string         `json:"contentRef,omitempty"`
	Source           string         `json:"source"`
	ImportMethod     string         `json:"importMethod,omitempty"`
	SourceProvider   string         `json:"sourceProvider,omitempty"`
	SourceVersion    string         `json:"sourceVersion,omitempty"`
	SourceURL        string         `json:"sourceUrl,omitempty"`
	ContentHash      string         `json:"contentHash,omitempty"`
	ExpansionID      string         `json:"expansionId,omitempty"`
	ExpansionVersion int64          `json:"expansionVersion,omitempty"`
	IsPrimary        bool           `json:"isPrimary"`
	Available        bool           `json:"available"`
	AddedAt          time.Time      `json:"addedAt"`
}
type Note struct {
	ID        string    `json:"id"`
	PaperID   string    `json:"paperId"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type Data struct {
	Folders     []Folder     `json:"folders"`
	Papers      []Paper      `json:"papers"`
	Attachments []Attachment `json:"attachments"`
	Notes       []Note       `json:"notes"`
}

func EmptyData() Data {
	return Data{Folders: []Folder{}, Papers: []Paper{}, Attachments: []Attachment{}, Notes: []Note{}}
}
func Pointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func WorkMetadata(work orm.AcademicWork) Metadata {
	m := Metadata{Title: work.CanonicalTitle, Authors: []string{}, Venue: work.Venue, Year: work.PublicationYear, Abstract: work.Abstract, Identifiers: Identifiers{DOI: work.DOINormalized, Arxiv: work.ArxivIDBase}}
	_ = json.Unmarshal(work.AuthorsJSON, &m.Authors)
	if m.Authors == nil {
		m.Authors = []string{}
	}
	return m
}
func mergeMetadata(entry orm.PaperLibraryEntry, work orm.AcademicWork, source json.RawMessage) (Metadata, error) {
	m := WorkMetadata(work)
	if len(source) > 0 {
		var value struct {
			Metadata
			CitationCountSnake *int64 `json:"citation_count"`
		}
		if err := json.Unmarshal(source, &value); err != nil {
			return m, err
		}
		if value.Title != "" {
			m.Title = value.Title
		}
		if len(value.Authors) > 0 {
			m.Authors = TrimAuthors(value.Authors)
		}
		if value.Year > 0 {
			m.Year = value.Year
		}
		if value.Venue != "" {
			m.Venue = value.Venue
		}
		if value.Abstract != "" {
			m.Abstract = value.Abstract
		}
		if value.CitationCount != nil {
			m.CitationCount = value.CitationCount
		}
		if value.CitationCountSnake != nil {
			m.CitationCount = value.CitationCountSnake
		}
	}
	if len(entry.MetadataJSON) > 0 {
		if err := json.Unmarshal(entry.MetadataJSON, &m); err != nil {
			return m, err
		}
	}
	m.Identifiers = Identifiers{DOI: work.DOINormalized, Arxiv: work.ArxivIDBase}
	if m.Authors == nil {
		m.Authors = []string{}
	}
	return m, nil
}

// Shared arXiv identity can have distinct publication DOIs. Titles are never proof.
func IdentityConflict(left, right Identifiers) bool {
	a, b := ArxivBase(left.Arxiv), ArxivBase(right.Arxiv)
	if a != "" && b != "" {
		return a != b
	}
	d, e := DOI(left.DOI), DOI(right.DOI)
	return d != "" && e != "" && d != e
}
func ImportMethod(entryType string) string {
	switch entryType {
	case "pdf_upload":
		return "upload"
	case "bibliography":
		return "bibliography"
	case "paper_link":
		return "link"
	case "paper_expansion":
		return "expansion"
	case "single_reference", "paper_references", "collection_references":
		return "references"
	case "conversation_sources":
		return "conversation"
	default:
		return ""
	}
}
func TrimAuthors(authors []string) []string {
	out := []string{}
	for _, a := range authors {
		a = strings.TrimSpace(a)
		if strings.ContainsFunc(a, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
			out = append(out, a)
		}
	}
	return out
}
