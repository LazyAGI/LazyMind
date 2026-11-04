package academic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/academic/arxivsource"
	"lazymind/core/artifact"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

type conversationPaperOriginal struct {
	FileID string `json:"file_id"`
	SHA256 string `json:"sha256"`
}

type conversationPDFManifest struct {
	FileID       string `json:"file_id"`
	SHA256       string `json:"content_sha256"`
	Source       string `json:"source"`
	URL          string `json:"source_url"`
	RequestedURL string `json:"requested_url"`
	ParseStatus  string `json:"parse_status"`
}

var chatPaperFileID = regexp.MustCompile(`^fr_[a-f0-9]{12}$`)
var chatPaperHash = regexp.MustCompile(`^[a-f0-9]{64}$`)
var paperBodyStart = regexp.MustCompile(`(?im)^\s*(?:abstract|references|bibliography)\b`)

func sourceConversationRoots(ctx context.Context, batch orm.PaperImportBatch) ([]string, error) {
	source := sourcePolicy(batch).Context
	if batch.EntryType != "conversation_sources" || source.ConversationID == "" || source.TaskID != "" || source.WorkflowSessionID != "" {
		return nil, nil
	}
	roots := artifact.ConversationAgentWorkspaceRoots(batch.CreatedBy, source.ConversationID)
	if len(roots) == 0 {
		return nil, nil
	}
	var count int64
	if err := store.DB().WithContext(ctx).Model(&orm.Conversation{}).Where("id=? AND create_user_id=? AND deleted_at IS NULL", source.ConversationID, batch.CreatedBy).Count(&count).Error; err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, nil
	}
	return roots, nil
}

func readPaperResource(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, errors.New("invalid conversation file resource")
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

func conversationOriginalCandidate(ctx context.Context, batch orm.PaperImportBatch, ref PaperRef, requiredVersion string, exactDOI bool, originals *doc.OriginalService) (savedImportCandidate, error) {
	// A DOI explicitly requesting a publication cannot be satisfied by a
	// conversation PDF with no verified edition information.
	if exactDOI || (ref.Title == "" && ref.DOI == "" && ref.ArxivID == "") {
		return savedImportCandidate{}, nil
	}
	roots, err := sourceConversationRoots(ctx, batch)
	if err != nil {
		return savedImportCandidate{}, err
	}
	for _, path := range roots {
		root, err := os.OpenRoot(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return savedImportCandidate{}, err
		}
		candidate, err := findPaperResource(ctx, root, batch, ref, requiredVersion, originals)
		root.Close()
		if err != nil || candidate.ConversationOriginal != nil {
			return candidate, err
		}
		// The main-Agent runtime selects the short layout when present.
		break
	}
	return savedImportCandidate{}, nil
}

func findPaperResource(ctx context.Context, root *os.Root, batch orm.PaperImportBatch, ref PaperRef, requiredVersion string, originals *doc.OriginalService) (savedImportCandidate, error) {
	dir, err := root.Open("file-resources")
	if errors.Is(err, os.ErrNotExist) {
		return savedImportCandidate{}, nil
	}
	if err != nil {
		return savedImportCandidate{}, err
	}
	defer dir.Close()
	for {
		if err := ctx.Err(); err != nil {
			return savedImportCandidate{}, err
		}
		entries, err := dir.ReadDir(32)
		if err != nil && !errors.Is(err, io.EOF) {
			return savedImportCandidate{}, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || !chatPaperFileID.MatchString(entry.Name()) {
				continue
			}
			base := filepath.Join("file-resources", entry.Name())
			raw, e := readPaperResource(root, filepath.Join(base, "manifest.json"), 32<<10)
			if e != nil {
				continue
			}
			var manifest conversationPDFManifest
			if json.Unmarshal(raw, &manifest) != nil || manifest.FileID != entry.Name() || manifest.Source != "web" || manifest.ParseStatus != "ready" || !chatPaperHash.MatchString(manifest.SHA256) {
				continue
			}
			address := firstNonEmpty(manifest.RequestedURL, manifest.URL)
			source, e := url.Parse(address)
			if e != nil || !zoteroAllowedURL(source, false) {
				continue
			}
			id, _ := arxivsource.Parse(address)
			if requiredVersion != "" && (id.BaseID != ref.ArxivID || id.Version != requiredVersion) {
				continue
			}
			head, e := readPaperResource(root, filepath.Join(base, "parsed.md"), 12<<10)
			if e != nil || !paperResourceOpeningMatches(ref, string(head), requiredVersion) {
				continue
			}
			original, e := acquireConversationOriginal(ctx, root, originals, sourceImportCaller(batch), conversationPaperOriginal{entry.Name(), manifest.SHA256})
			if e != nil {
				return savedImportCandidate{}, e
			}
			pages, e := verifyConversationOriginal(ctx, originals, sourceImportCaller(batch), original)
			if releaseErr := originals.ReleaseOriginal(context.WithoutCancel(ctx), sourceImportCaller(batch), original.FileRef); releaseErr != nil && e == nil {
				e = releaseErr
			}
			if e != nil {
				return savedImportCandidate{}, e
			}
			if !paperResourceOpeningMatches(ref, pages[0].Text, requiredVersion) {
				continue
			}
			// Do not persist expiring redirect credentials.
			source.RawQuery, source.Fragment = "", ""
			candidate := FulltextCandidate{URL: source.String(), SourceType: "conversation_pdf", SourceProvider: "web", ExpectedMIME: "application/pdf"}
			if requiredVersion != "" {
				candidate.URL, candidate.SourceProvider, candidate.Version = id.PDFURL(), "arxiv", id.Version
			}
			ref.PDFURL, ref.SourceProvider, ref.SourceVersion = candidate.URL, candidate.SourceProvider, candidate.Version
			return savedImportCandidate{FulltextCandidate: candidate, VerifiedPaper: &ref, ExpectedHash: manifest.SHA256, ConversationOriginal: &conversationPaperOriginal{entry.Name(), manifest.SHA256}}, nil
		}
		if errors.Is(err, io.EOF) {
			return savedImportCandidate{}, nil
		}
	}
}

func paperResourceOpeningMatches(ref PaperRef, opening, requiredVersion string) bool {
	if _, page, ok := strings.Cut(opening, "<!-- page:1 -->"); ok {
		opening = page
	}
	opening, _, _ = strings.Cut(opening, "<!-- page:")
	runes := []rune(opening)
	opening = string(runes[:min(len(runes), 2500)])
	front := opening
	if boundary := paperBodyStart.FindStringIndex(front); boundary != nil {
		front = front[:boundary[0]]
	}
	doi, arxiv, version := extractStrongIDs(front)
	if requiredVersion != "" && (arxiv != ref.ArxivID || version != requiredVersion) {
		return false
	}
	if arxiv != "" && ref.ArxivID != "" && arxiv != ref.ArxivID || doi != "" && ref.DOI != "" && doi != ref.DOI && (arxiv == "" || arxiv != ref.ArxivID) {
		return false
	}
	quality := checkPaperQuality(ref, []ContentPage{{Page: 1, Text: opening}}, "conversation", "verified")
	if quality.Material == "other" || quality.Text != "usable" {
		return false
	}
	if (arxiv != "" && arxiv == ref.ArxivID) || (doi != "" && doi == ref.DOI) {
		return true
	}
	normalized := " " + NormalizeTitle(front) + " "
	title := NormalizeTitle(ref.Title)
	if title == "" || !strings.Contains(normalized, " "+title+" ") {
		return false
	}
	for _, author := range ref.Authors {
		name := NormalizeTitle(author)
		if name != "" && strings.Contains(normalized, " "+name+" ") {
			return true
		}
	}
	return false
}

func acquireConversationOriginal(ctx context.Context, root *os.Root, originals *doc.OriginalService, caller doc.DatasetCatalogCaller, selected conversationPaperOriginal) (doc.OriginalRef, error) {
	if !chatPaperFileID.MatchString(selected.FileID) || !chatPaperHash.MatchString(selected.SHA256) {
		return doc.OriginalRef{}, errors.New("invalid conversation PDF snapshot")
	}
	f, err := root.Open(filepath.Join("file-resources", selected.FileID, "original.pdf"))
	if err != nil {
		return doc.OriginalRef{}, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return doc.OriginalRef{}, err
	}
	if !stat.Mode().IsRegular() || stat.Size() <= 0 || stat.Size() > maxPaperBytes {
		return doc.OriginalRef{}, errors.New("invalid conversation PDF original")
	}
	ref, err := originals.AcquireOriginal(ctx, caller, doc.AcquireOriginalRequest{Filename: "paper.pdf", ContentType: "application/pdf", Reader: io.LimitReader(f, maxPaperBytes+1)})
	if err == nil && (ref.ContentType != "application/pdf" || ref.ContentSHA256 != selected.SHA256 || ref.Size > maxPaperBytes) {
		_ = originals.ReleaseOriginal(context.WithoutCancel(ctx), caller, ref.FileRef)
		return doc.OriginalRef{}, errors.New("conversation PDF content changed")
	}
	return ref, err
}

func verifyConversationOriginal(ctx context.Context, originals *doc.OriginalService, caller doc.DatasetCatalogCaller, ref doc.OriginalRef) ([]ContentPage, error) {
	f, err := originals.OpenOriginal(ctx, caller, ref.FileRef)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	pages, _, err := parsePaperPDF(ctx, f.Name(), ref.ContentSHA256)
	return pages, err
}

func acquireSavedConversationOriginal(ctx context.Context, originals *doc.OriginalService, caller doc.DatasetCatalogCaller, batch orm.PaperImportBatch, selected savedImportCandidate) (doc.OriginalRef, error) {
	roots, err := sourceConversationRoots(ctx, batch)
	if err != nil {
		return doc.OriginalRef{}, err
	}
	for _, path := range roots {
		root, err := os.OpenRoot(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return doc.OriginalRef{}, err
		}
		ref, err := acquireConversationOriginal(ctx, root, originals, caller, *selected.ConversationOriginal)
		root.Close()
		ref.FinalURL = selected.URL
		return ref, err
	}
	return doc.OriginalRef{}, gorm.ErrRecordNotFound
}
