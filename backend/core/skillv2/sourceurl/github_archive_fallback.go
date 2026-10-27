package sourceurl

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func githubArchiveFallbackAllowed(err error) bool {
	return strings.Contains(err.Error(), "GitHub API rate limit reached")
}

// The archive service does not consume REST API quota. Validate refs using HEAD
// so a rate-limited import can reach archive fallback without guessing where a
// slash-containing branch name ends, or downloading archives for invalid refs.
func resolveGitHubArchiveFallback(ctx context.Context, client *http.Client, source githubSourceURL, original error) (GitHubResolution, bool, error) {
	if !source.refDiscoveryAttempted {
		if advertised, err := resolveGitHubAdvertisedRef(ctx, client, source); err == nil {
			return advertised, true, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return GitHubResolution{}, true, err
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	safeClient := *client
	if safeClient.Timeout == 0 {
		safeClient.Timeout = 10 * time.Second
	}
	originalRedirect := safeClient.CheckRedirect
	safeClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		req.Header.Del("Authorization")
		if originalRedirect != nil {
			err := originalRedirect(req, via)
			req.Header.Del("Authorization")
			return err
		}
		if len(via) >= 10 {
			return http.ErrUseLastResponse
		}
		return nil
	}
	treeParts := source.treeParts
	if len(treeParts) == 0 {
		treeParts = []string{"HEAD"}
	}
	// Bound the entire compatibility probe, including all candidate refs.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for split := len(treeParts); split > 0; split-- {
		ref := strings.Join(treeParts[:split], "/")
		archive := githubArchiveURL(source.owner, source.repository, ref)
		request, err := http.NewRequestWithContext(ctx, http.MethodHead, archive, nil)
		if err != nil {
			return GitHubResolution{}, true, err
		}
		request.Header.Set("User-Agent", "LazyMind-Skill-Importer")
		response, err := safeClient.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return GitHubResolution{}, true, ctx.Err()
			}
			return GitHubResolution{}, true, original
		}
		_ = response.Body.Close()
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusUnprocessableEntity {
			continue
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return GitHubResolution{DownloadURL: archive, Revision: ref, Owner: source.owner, Repository: source.repository, PathPrefix: strings.Join(treeParts[split:], "/"), ExplicitDirectory: source.explicitDirectory}, true, nil
		}
		return GitHubResolution{}, true, original
	}
	return GitHubResolution{}, true, original
}
