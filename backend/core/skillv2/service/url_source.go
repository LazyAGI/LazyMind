package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	"lazymind/core/skillv2/sourceurl"
)

// ResolveURLSource is shared by personal imports, market publishing, and
// workflow installs so every entry point supports the same website adapters.
func ResolveURLSource(ctx context.Context, rawURL string) (SourceInput, error) {
	rawURL = strings.TrimSpace(rawURL)
	resolved, err := sourceurl.ResolveImportURL(ctx, rawURL, &http.Client{Timeout: 15 * time.Second}, sourceurl.GitHubAPIBaseURL)
	if err != nil {
		return SourceInput{}, err
	}
	return SourceInput{
		Type: "url", URL: resolved.DownloadURL, SourceURL: rawURL,
		ResolvedSourceURL: resolved.ResolvedSourceURL, CanonicalName: resolved.CanonicalName,
		PathPrefix: resolved.PathPrefix, PathPrefixResolved: true,
	}, nil
}
