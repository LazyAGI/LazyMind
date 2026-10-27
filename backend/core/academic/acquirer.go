package academic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	"lazymind/core/academic/arxivsource"
	"lazymind/core/doc"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

const maxPaperBytes int64 = 64 << 20

func canonicalPaperURLs(candidate FulltextCandidate) ([]string, error) {
	if !strings.EqualFold(strings.TrimSpace(candidate.SourceProvider), "arxiv") {
		return []string{candidate.URL}, nil
	}
	identifier, err := arxivsource.Parse(candidate.URL)
	if err != nil {
		return nil, err
	}
	if version := strings.TrimSpace(candidate.Version); version != "" {
		versioned, parseErr := arxivsource.Parse(identifier.BaseID + version)
		if parseErr != nil || versioned.Version == "" || versioned.Version != strings.ToLower(version) {
			return nil, fmt.Errorf("arXiv candidate has invalid version %q", version)
		}
		if identifier.Version != "" && identifier.Version != versioned.Version {
			return nil, fmt.Errorf("arXiv version conflict: URL specifies %s, candidate specifies %s", identifier.Version, versioned.Version)
		}
		identifier.Version = versioned.Version
	}
	return []string{identifier.PDFURL()}, nil
}

func validateArxivPDFRedirect(candidate FulltextCandidate, target string) error {
	urls, err := canonicalPaperURLs(candidate)
	if err != nil {
		return err
	}
	expected, err := arxivsource.Parse(urls[0])
	if err != nil {
		return err
	}
	targetURL, err := url.Parse(target)
	if err != nil || targetURL.Scheme != "https" || !strings.HasPrefix(targetURL.Path, "/pdf/") {
		return errors.New("arXiv PDF redirect must use an official HTTPS PDF URL")
	}
	switch strings.ToLower(targetURL.Hostname()) {
	case "arxiv.org", "www.arxiv.org", "export.arxiv.org":
	default:
		return errors.New("arXiv PDF redirect points outside the official arXiv hosts")
	}
	actual, err := arxivsource.Parse(target)
	if err != nil {
		return err
	}
	if actual.BaseID != expected.BaseID || (expected.Version != "" && actual.Version != expected.Version) {
		return fmt.Errorf("arXiv PDF redirect changed the paper or version: expected %s, received %s", expected.ID(), actual.ID())
	}
	return nil
}

func acquireCandidateOriginal(ctx context.Context, svc *doc.OriginalService, caller doc.DatasetCatalogCaller, candidate FulltextCandidate) (doc.OriginalRef, error) {
	urls, err := canonicalPaperURLs(candidate)
	if err != nil {
		return doc.OriginalRef{}, err
	}
	var lastErr error
	for _, address := range urls {
		request := doc.AcquireRemoteOriginalRequest{URL: address, MaxBytes: maxPaperBytes}
		if candidate.SourceType != "reference_page" && candidate.SourceProvider != "web" {
			request.DisplayName, request.ExpectedMIME = "paper.pdf", "application/pdf"
		}
		if candidate.SourceType == "openalex_hosted" {
			target, e := url.Parse(address)
			if e != nil || target.Scheme != "https" || target.Host != "content.openalex.org" || target.User != nil || !regexp.MustCompile(`^/works/W[0-9]+\.pdf$`).MatchString(target.Path) {
				return doc.OriginalRef{}, errors.New("invalid OpenAlex hosted content URL")
			}
			key, e := modelconfig.LoadOpenAlexAPIKey(ctx, store.DB(), caller.UserID)
			if e != nil {
				return doc.OriginalRef{}, e
			}
			if key == "" {
				return doc.OriginalRef{}, errors.New("OpenAlex 托管全文需要有效的 API Key")
			}
			request.PrivateQuery = map[string]string{"api_key": key}
		}
		ref, err := svc.AcquireRemoteOriginal(ctx, caller, request)
		if err == nil && strings.EqualFold(candidate.SourceProvider, "arxiv") {
			err = validateArxivPDFRedirect(candidate, ref.FinalURL)
		}
		if err == nil {
			return ref, nil
		}
		if ref.FileRef != "" {
			_ = svc.ReleaseOriginal(ctx, caller, ref.FileRef)
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return doc.OriginalRef{}, lastErr
}

// The shared document service owns HTTP, source validation and temporary originals.
// This adapter only materializes the verified bytes in the paper cache's staging directory.
func acquirePDF(ctx context.Context, user string, candidate FulltextCandidate) (path string, size int64, err error) {
	if _, e := canonicalPaperURLs(candidate); e != nil {
		return "", 0, e
	}
	svc, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: store.DB()})
	if err != nil {
		return "", 0, err
	}
	caller := doc.DatasetCatalogCaller{UserID: user}
	original, err := acquireCandidateOriginal(ctx, svc, caller, candidate)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = svc.ReleaseOriginal(context.WithoutCancel(ctx), caller, original.FileRef) }()
	source, err := svc.OpenOriginal(ctx, caller, original.FileRef)
	if err != nil {
		return "", 0, err
	}
	defer source.Close()
	directory, _ := ctx.Value(paperPDFDownloadDir{}).(string)
	target, err := os.CreateTemp(directory, "lazymind-paper-*.pdf")
	if err != nil {
		return "", 0, err
	}
	path = target.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	hash := sha256.New()
	size, err = io.Copy(io.MultiWriter(target, hash), source)
	closeErr := target.Close()
	if err != nil {
		return path, 0, err
	}
	if closeErr != nil {
		return path, 0, closeErr
	}
	if size != original.Size || hex.EncodeToString(hash.Sum(nil)) != original.ContentSHA256 {
		return path, 0, errors.New("original PDF changed before caching")
	}
	return path, size, nil
}
