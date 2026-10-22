package sourceurl

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

const GitHubAPIBaseURL = "https://api.github.com"

type GitHubResolution struct {
	DownloadURL          string
	PathPrefix           string
	PathPrefixCandidates []string
	Owner                string
	Repository           string
	Revision             string
	ExplicitDirectory    bool
}

// ResolveGitHubPageURL recognizes repository, tree, and SKILL.md page/raw URLs.
// GitHub UI query parameters and fragments do not affect the selected source.
// Direct release ZIP assets and non-GitHub URLs remain direct downloads.
func ResolveGitHubPageURL(ctx context.Context, parsed *url.URL, client *http.Client, apiBaseURL string) (resolution GitHubResolution, matched bool, err error) {
	return resolveGitHubPageURL(ctx, parsed, client, apiBaseURL, false)
}

// ResolveGitHubImportURL allows the interactive importer to use a validated
// mutable archive ref when REST metadata is rate limited. Build/lock consumers
// use ResolveGitHubPageURL instead so their successful resolutions stay pinned.
func ResolveGitHubImportURL(ctx context.Context, parsed *url.URL, client *http.Client, apiBaseURL string) (GitHubResolution, bool, error) {
	return resolveGitHubPageURL(ctx, parsed, client, apiBaseURL, true)
}

func resolveGitHubPageURL(ctx context.Context, parsed *url.URL, client *http.Client, apiBaseURL string, allowArchiveFallback bool) (resolution GitHubResolution, matched bool, err error) {
	source, matched, err := parseGitHubSourceURL(parsed)
	if err != nil || !matched {
		return GitHubResolution{}, matched, err
	}
	owner, repository := source.owner, source.repository
	if source.archiveRef != "" {
		return GitHubResolution{DownloadURL: githubArchiveURL(owner, repository, source.archiveRef), Owner: owner, Repository: repository, Revision: source.archiveRef}, true, nil
	}
	if len(source.treeParts) == 0 {
		ref, err := resolveGitHubDefaultBranch(ctx, client, apiBaseURL, owner, repository)
		if err != nil {
			if allowArchiveFallback && githubArchiveFallbackAllowed(err) {
				return resolveGitHubArchiveFallback(ctx, client, source, err)
			}
			return GitHubResolution{}, true, err
		}
		commit, err := resolveGitHubCommit(ctx, client, apiBaseURL, owner, repository, ref)
		if err != nil {
			if allowArchiveFallback && githubArchiveFallbackAllowed(err) {
				return resolveGitHubArchiveFallback(ctx, client, source, err)
			}
			return GitHubResolution{}, true, err
		}
		return GitHubResolution{DownloadURL: githubArchiveURL(owner, repository, commit), Owner: owner, Repository: repository, Revision: commit}, true, nil
	}
	ref, pathPrefix, err := resolveGitHubTreeRef(ctx, client, apiBaseURL, owner, repository, source.treeParts)
	if err != nil {
		if allowArchiveFallback && githubArchiveFallbackAllowed(err) {
			return resolveGitHubArchiveFallback(ctx, client, source, err)
		}
		return GitHubResolution{}, true, err
	}
	return GitHubResolution{DownloadURL: githubArchiveURL(owner, repository, ref), PathPrefix: pathPrefix, Owner: owner, Repository: repository, Revision: ref, ExplicitDirectory: source.explicitDirectory}, true, nil
}

// ResolveGitHubPageURLFromResolvedArchive reconstructs a source resolution using
// a lock file's archive, without looking up mutable branches through the API.
func ResolveGitHubPageURLFromResolvedArchive(parsed *url.URL, resolvedArchiveURL string) (resolution GitHubResolution, matched bool, err error) {
	source, matched, err := parseGitHubSourceURL(parsed)
	if err != nil || !matched {
		return GitHubResolution{}, matched, err
	}
	owner, repository := source.owner, source.repository
	if source.archiveRef != "" {
		return GitHubResolution{DownloadURL: githubArchiveURL(owner, repository, source.archiveRef), Owner: owner, Repository: repository, Revision: source.archiveRef}, true, nil
	}
	ref, err := githubResolvedArchiveRef(resolvedArchiveURL, owner, repository)
	if err != nil {
		return GitHubResolution{}, true, err
	}
	result := GitHubResolution{DownloadURL: resolvedArchiveURL, Owner: owner, Repository: repository, Revision: ref, ExplicitDirectory: source.explicitDirectory}
	treeParts := source.treeParts
	if len(treeParts) == 0 {
		return result, true, nil
	}
	refParts := strings.Split(ref, "/")
	if len(ref) == 40 && isHex(ref) && len(treeParts) > 1 && treeParts[0] != ref {
		result.PathPrefix = strings.Join(treeParts[1:], "/")
		result.PathPrefixCandidates = githubTreePathPrefixCandidates(treeParts)
		return result, true, nil
	}
	if len(ref) == 40 && isHex(ref) && len(treeParts) == 1 && treeParts[0] != ref {
		return result, true, nil
	}
	if len(treeParts) < len(refParts) {
		return GitHubResolution{}, true, fmt.Errorf("locked GitHub archive ref does not match source URL")
	}
	for index := range refParts {
		if treeParts[index] != refParts[index] {
			return GitHubResolution{}, true, fmt.Errorf("locked GitHub archive ref does not match source URL")
		}
	}
	result.PathPrefix = strings.Join(treeParts[len(refParts):], "/")
	return result, true, nil
}

type githubSourceURL struct {
	owner, repository string
	archiveRef        string
	treeParts         []string
	explicitDirectory bool
}

func parseGitHubSourceURL(parsed *url.URL) (githubSourceURL, bool, error) {
	if parsed == nil {
		return githubSourceURL{}, false, nil
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if host != "github.com" && host != "raw.githubusercontent.com" {
		return githubSourceURL{}, false, nil
	}
	parts, err := githubURLPathParts(parsed.EscapedPath())
	if err != nil {
		return githubSourceURL{}, true, err
	}
	if len(parts) < 2 {
		return githubSourceURL{}, true, fmt.Errorf("GitHub URL must identify a repository")
	}
	if host == "github.com" && isGitHubReleaseZip(parts) {
		return githubSourceURL{}, false, nil
	}
	if parsed.User != nil || parsed.Port() != "" {
		return githubSourceURL{}, true, fmt.Errorf("GitHub URL must not contain credentials or a port")
	}
	source := githubSourceURL{owner: parts[0], repository: strings.TrimSuffix(parts[1], ".git")}
	if !isValidGitHubName(source.owner) || !isValidGitHubName(source.repository) {
		return githubSourceURL{}, true, fmt.Errorf("GitHub URL must identify a repository")
	}
	if host == "raw.githubusercontent.com" {
		if len(parts) < 4 || !strings.EqualFold(parts[len(parts)-1], "SKILL.md") {
			return githubSourceURL{}, true, fmt.Errorf("GitHub raw URL must point to SKILL.md")
		}
		source.treeParts = parts[2 : len(parts)-1]
		source.explicitDirectory = true
		return source, true, nil
	}
	if ref, ok := githubArchiveRef(parts); ok {
		source.archiveRef = ref
		return source, true, nil
	}
	if len(parts) == 2 {
		return source, true, nil
	}
	if len(parts) >= 4 && parts[2] == "tree" {
		source.treeParts = parts[3:]
		source.explicitDirectory = true
		return source, true, nil
	}
	if len(parts) >= 5 && parts[2] == "blob" && strings.EqualFold(parts[len(parts)-1], "SKILL.md") {
		source.treeParts = parts[3 : len(parts)-1]
		source.explicitDirectory = true
		return source, true, nil
	}
	return githubSourceURL{}, true, fmt.Errorf("GitHub URL must point to a repository, /tree/<ref>/<skill-path>, or SKILL.md")
}

func githubTreePathPrefixCandidates(treeParts []string) []string {
	if len(treeParts) <= 1 {
		return nil
	}
	candidates := make([]string, 0, len(treeParts)-1)
	for split := 1; split < len(treeParts); split++ {
		candidates = append(candidates, strings.Join(treeParts[split:], "/"))
	}
	return candidates
}

func githubResolvedArchiveRef(raw, owner, repository string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.") != "github.com" {
		return "", fmt.Errorf("locked GitHub archive URL is invalid")
	}
	if parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("locked GitHub archive URL is invalid")
	}
	escapedParts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(escapedParts) < 4 || escapedParts[2] != "archive" {
		return "", fmt.Errorf("locked GitHub archive URL is invalid")
	}
	resolvedOwner, ownerErr := url.PathUnescape(escapedParts[0])
	resolvedRepository, repositoryErr := url.PathUnescape(escapedParts[1])
	if ownerErr != nil || repositoryErr != nil || !strings.EqualFold(resolvedOwner, owner) || !strings.EqualFold(strings.TrimSuffix(resolvedRepository, ".git"), repository) {
		return "", fmt.Errorf("locked GitHub archive URL does not match source repository")
	}
	escapedRef := strings.Join(escapedParts[3:], "/")
	if !strings.HasSuffix(strings.ToLower(escapedRef), ".zip") {
		return "", fmt.Errorf("locked GitHub archive URL is invalid")
	}
	escapedRef = escapedRef[:len(escapedRef)-len(".zip")]
	ref, err := url.PathUnescape(escapedRef)
	if err != nil {
		return "", fmt.Errorf("locked GitHub archive URL is invalid")
	}
	refParts := strings.Split(ref, "/")
	if len(refParts) >= 3 && refParts[0] == "refs" && (refParts[1] == "heads" || refParts[1] == "tags") {
		refParts = refParts[2:]
	}
	for _, part := range refParts {
		if part == "" || part == "." || part == ".." || strings.ContainsRune(part, '\\') || strings.ContainsRune(part, 0) {
			return "", fmt.Errorf("locked GitHub archive URL is invalid")
		}
	}
	return strings.Join(refParts, "/"), nil
}

func isGitHubReleaseZip(parts []string) bool {
	return len(parts) >= 6 && parts[2] == "releases" && parts[3] == "download" && strings.HasSuffix(strings.ToLower(parts[len(parts)-1]), ".zip")
}

func githubArchiveRef(parts []string) (string, bool) {
	if len(parts) < 4 || parts[2] != "archive" {
		return "", false
	}
	refParts := append([]string(nil), parts[3:]...)
	if len(refParts) >= 3 && refParts[0] == "refs" && (refParts[1] == "heads" || refParts[1] == "tags") {
		refParts = refParts[2:]
	} else if len(refParts) != 1 {
		return "", false
	}
	last := refParts[len(refParts)-1]
	if !strings.HasSuffix(last, ".zip") {
		return "", false
	}
	refParts[len(refParts)-1] = strings.TrimSuffix(last, ".zip")
	if refParts[len(refParts)-1] == "" {
		return "", false
	}
	return strings.Join(refParts, "/"), true
}

func githubURLPathParts(escapedPath string) ([]string, error) {
	rawParts := strings.Split(strings.Trim(escapedPath, "/"), "/")
	if len(rawParts) == 1 && rawParts[0] == "" {
		return nil, fmt.Errorf("GitHub URL must identify a repository")
	}
	parts := make([]string, 0, len(rawParts))
	for _, rawPart := range rawParts {
		part, err := url.PathUnescape(rawPart)
		if err != nil || part == "" || part == "." || part == ".." || strings.ContainsAny(part, `/\`) || strings.ContainsRune(part, 0) {
			return nil, fmt.Errorf("GitHub URL contains an invalid path segment")
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func resolveGitHubDefaultBranch(ctx context.Context, client *http.Client, apiBaseURL, owner, repository string) (string, error) {
	var response struct {
		DefaultBranch string `json:"default_branch"`
	}
	endpoint := strings.TrimRight(apiBaseURL, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository)
	status, err := githubAPIGet(ctx, client, endpoint, &response)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 || strings.TrimSpace(response.DefaultBranch) == "" {
		return "", fmt.Errorf("GitHub repository default branch could not be resolved")
	}
	return response.DefaultBranch, nil
}

func resolveGitHubTreeRef(ctx context.Context, client *http.Client, apiBaseURL, owner, repository string, treeParts []string) (string, string, error) {
	// A full commit SHA identifies the ref boundary without an API lookup.
	// Short SHAs and branch/tag names still need resolution (refs may contain /).
	if len(treeParts) > 0 && len(treeParts[0]) == 40 {
		if isHex(treeParts[0]) {
			return treeParts[0], strings.Join(treeParts[1:], "/"), nil
		}
	}
	for split := len(treeParts); split > 0; split-- {
		ref := strings.Join(treeParts[:split], "/")
		pathPrefix := strings.Join(treeParts[split:], "/")
		commit, status, err := resolveGitHubCommitStatus(ctx, client, apiBaseURL, owner, repository, ref)
		if err != nil {
			return "", "", err
		}
		if status == http.StatusNotFound || status == http.StatusUnprocessableEntity {
			continue
		}
		if status >= 200 && status < 300 {
			return commit, pathPrefix, nil
		}
		return "", "", fmt.Errorf("GitHub ref lookup failed with HTTP status %d", status)
	}
	return "", "", fmt.Errorf("GitHub URL ref could not be resolved")
}

func isHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

func resolveGitHubCommit(ctx context.Context, client *http.Client, apiBaseURL, owner, repository, ref string) (string, error) {
	commit, status, err := resolveGitHubCommitStatus(ctx, client, apiBaseURL, owner, repository, ref)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 || strings.TrimSpace(commit) == "" {
		return "", fmt.Errorf("GitHub ref lookup failed with HTTP status %d", status)
	}
	return commit, nil
}

func resolveGitHubCommitStatus(ctx context.Context, client *http.Client, apiBaseURL, owner, repository, ref string) (string, int, error) {
	var response struct {
		SHA string `json:"sha"`
	}
	endpoint := strings.TrimRight(apiBaseURL, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) + "/commits/" + url.PathEscape(ref)
	status, err := githubAPIGet(ctx, client, endpoint, &response)
	if err != nil {
		return "", 0, err
	}
	return strings.TrimSpace(response.SHA), status, nil
}

func githubArchiveURL(owner, repository, ref string) string {
	return "https://github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) + "/archive/" + url.PathEscape(ref) + ".zip"
}

func isValidGitHubName(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}
