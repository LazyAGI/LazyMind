package academic

import (
	"context"
	"lazymind/core/doc"
)

// Candidate identity and mirror policy belong to academic; byte acquisition and
// validation belong to the shared Core original service.
func candidateOriginalURLs(candidate FulltextCandidate) []string {
	if candidate.SourceProvider != "arxiv" {
		return []string{candidate.URL}
	}
	base, version := NormalizeArxivID(candidate.URL)
	if base == "" {
		return []string{candidate.URL}
	}
	fixed := base + version
	return []string{"https://arxiv.org/pdf/" + fixed, "https://arxiv.org/pdf/" + fixed + ".pdf", "https://export.arxiv.org/pdf/" + fixed}
}
func acquireCandidateOriginal(ctx context.Context, svc *doc.OriginalService, caller doc.DatasetCatalogCaller, candidate FulltextCandidate) (doc.OriginalRef, error) {
	var lastErr error
	for _, url := range candidateOriginalURLs(candidate) {
		request := doc.AcquireRemoteOriginalRequest{URL: url}
		if candidate.SourceType != "reference_page" && candidate.SourceProvider != "web" {
			request.DisplayName = "paper.pdf"
			request.ExpectedMIME = "application/pdf"
		}
		ref, err := svc.AcquireRemoteOriginal(ctx, caller, request)
		if err == nil {
			return ref, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return doc.OriginalRef{}, lastErr
}
