package sourceurl

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// One bounded smart-HTTP advertisement establishes all possible ref boundaries,
// including slash-containing branches and peeled annotated tags, without REST
// quota or generating an archive for each rejected path prefix.
func resolveGitHubAdvertisedRef(ctx context.Context, client *http.Client, source githubSourceURL) (GitHubResolution, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	endpoint := "https://github.com/" + url.PathEscape(source.owner) + "/" + url.PathEscape(source.repository) + ".git/info/refs?service=git-upload-pack"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return GitHubResolution{}, err
	}
	request.Header.Set("User-Agent", "LazyMind-skill-import")
	response, err := skillsSHHTTPClient(client, 15*time.Second, "github.com").Do(request)
	if err != nil {
		return GitHubResolution{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return GitHubResolution{}, fmt.Errorf("GitHub reference discovery failed with HTTP status %d", response.StatusCode)
	}
	reader := io.LimitReader(response.Body, 1<<20)
	refs := make(map[string]string)
	for {
		var prefix [4]byte
		if _, err := io.ReadFull(reader, prefix[:]); err != nil {
			return GitHubResolution{}, fmt.Errorf("GitHub reference advertisement is incomplete: %w", err)
		}
		length, err := strconv.ParseUint(string(prefix[:]), 16, 16)
		if err != nil || (length > 0 && length < 4) {
			return GitHubResolution{}, fmt.Errorf("GitHub reference advertisement has an invalid packet")
		}
		if length == 0 {
			if len(refs) > 0 {
				break
			}
			continue
		}
		packet := make([]byte, int(length)-4)
		if _, err := io.ReadFull(reader, packet); err != nil {
			return GitHubResolution{}, fmt.Errorf("GitHub reference advertisement is incomplete: %w", err)
		}
		ref, _, _ := strings.Cut(strings.TrimSuffix(string(packet), "\n"), "\x00")
		sha, name, found := strings.Cut(ref, " ")
		if found && len(sha) == 40 && isHex(sha) && strings.Trim(sha, "0") != "" && (name == "HEAD" || strings.HasPrefix(name, "refs/heads/") || strings.HasPrefix(name, "refs/tags/")) {
			refs[name] = sha
		}
	}
	parts := source.treeParts
	if len(parts) == 0 {
		if sha := refs["HEAD"]; sha != "" {
			return GitHubResolution{DownloadURL: githubArchiveURL(source.owner, source.repository, sha), Owner: source.owner, Repository: source.repository, Revision: sha}, nil
		}
	}
	for split := len(parts); split > 0; split-- {
		ref := strings.Join(parts[:split], "/")
		sha := refs["refs/heads/"+ref]
		if sha == "" {
			sha = refs["refs/tags/"+ref+"^{}"]
		}
		if sha == "" {
			sha = refs["refs/tags/"+ref]
		}
		if ref == "HEAD" && sha == "" {
			sha = refs["HEAD"]
		}
		if sha != "" {
			return GitHubResolution{DownloadURL: githubArchiveURL(source.owner, source.repository, sha), Owner: source.owner, Repository: source.repository, Revision: sha, PathPrefix: strings.Join(parts[split:], "/"), ExplicitDirectory: source.explicitDirectory}, nil
		}
	}
	return GitHubResolution{}, fmt.Errorf("GitHub URL ref was not advertised")
}
