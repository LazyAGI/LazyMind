package sourceurl

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"

	"lazymind/core/skillv2/skillpackage"
)

const (
	skillsSHPageLimit       = 2 << 20
	skillsSHDocumentLimit   = 128 << 10
	skillsSHDiscoveryBytes  = 8 << 20
	skillsSHDiscoveryFiles  = 128
	skillsSHPageSourceLinks = 8
	skillsSHArchiveLimit    = 100 << 20
	skillsSHArchiveFiles    = 4096
	skillsSHArchiveBytes    = 32 << 20
)

// SkillsSHResolution selects the actual repository directory, retaining the
// name advertised in SKILL.md rather than assuming the catalog slug is a path.
type SkillsSHResolution struct {
	GitHubSourceURL string
	CanonicalName   string
	PreparedPackage *PreparedPackage
}

// ResolveSkillsSHPageURL resolves skills.sh/{owner}/{repo}/{skill}. Catalog
// slugs are computed from frontmatter names (see vercel-labs/skills/src/blob.ts),
// and can differ from directory names. All content is subsequently retrieved
// from the selected GitHub directory, preserving binary supporting files.
func ResolveSkillsSHPageURL(ctx context.Context, parsed *url.URL, client *http.Client, apiBaseURL string) (SkillsSHResolution, bool, error) {
	if parsed == nil || !skillsSHHost(parsed.Hostname()) {
		return SkillsSHResolution{}, false, nil
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.Port() != "" {
		return SkillsSHResolution{}, true, fmt.Errorf("skills.sh URL must use HTTP(S) without credentials or a port")
	}
	parts, err := githubURLPathParts(parsed.EscapedPath())
	if err != nil || len(parts) != 3 || !isValidGitHubName(parts[0]) || !isValidGitHubName(parts[1]) || skillsSHSlug(parts[2]) != parts[2] || parts[2] == "" {
		return SkillsSHResolution{}, true, fmt.Errorf("skills.sh URL must identify /<owner>/<repository>/<skill>")
	}
	if err := ctx.Err(); err != nil {
		return SkillsSHResolution{}, true, err
	}
	owner, repository, slug := parts[0], parts[1], parts[2]
	reader := skillsSHReader{client: client}

	// Some catalog pages expose an exact source link. Verify its frontmatter
	// before using it, since a skill's own Markdown can contain unrelated links.
	pageURL := "https://www.skills.sh/" + strings.Join(parts, "/")
	if page, pageErr := reader.get(ctx, pageURL, skillsSHPageLimit, true); pageErr == nil {
		var selected SkillsSHResolution
		for _, source := range skillsSHPageSources(page, owner, repository) {
			resolved, _, resolveErr := ResolveGitHubImportURL(ctx, source, client, apiBaseURL)
			if resolveErr != nil {
				continue
			}
			document := "SKILL.md"
			if strings.EqualFold(path.Base(source.Path), "SKILL.md") {
				document = path.Base(source.Path)
			}
			name, readErr := reader.skillName(ctx, owner, repository, resolved.Revision, skillsSHJoinPath(resolved.PathPrefix, document))
			if readErr == nil && skillsSHSlug(name) == slug {
				candidate := skillsSHSelected(owner, repository, resolved.Revision, resolved.PathPrefix, name)
				if selected.GitHubSourceURL != "" && selected.GitHubSourceURL != candidate.GitHubSourceURL {
					return SkillsSHResolution{}, true, fmt.Errorf("skills.sh skill name is ambiguous; use an exact GitHub skill directory URL")
				}
				selected = candidate
			}
		}
		if selected.GitHubSourceURL != "" {
			return selected, true, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return SkillsSHResolution{}, true, err
	}

	// HEAD resolves the repository's real default branch without guessing main
	// or master. Resolve the commit before fetching its tree and documents so a
	// concurrent push cannot change the selected package between requests.
	revision, err := resolveGitHubCommit(ctx, client, apiBaseURL, owner, repository, "HEAD")
	if err != nil {
		if githubArchiveFallbackAllowed(err) {
			return resolveSkillsSHArchive(ctx, client, owner, repository, slug, "", err)
		}
		return SkillsSHResolution{}, true, fmt.Errorf("skills.sh GitHub source could not be resolved: %w", err)
	}
	if len(revision) != 40 || !isHex(revision) {
		return SkillsSHResolution{}, true, fmt.Errorf("skills.sh GitHub source returned an invalid commit")
	}
	var tree struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Mode string `json:"mode"`
		} `json:"tree"`
	}
	endpoint := strings.TrimRight(apiBaseURL, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) + "/git/trees/" + revision + "?recursive=1"
	status, err := GitHubAPIGet(ctx, client, endpoint, &tree)
	if err != nil {
		if githubArchiveFallbackAllowed(err) {
			return resolveSkillsSHArchive(ctx, client, owner, repository, slug, revision, err)
		}
		return SkillsSHResolution{}, true, fmt.Errorf("skills.sh repository tree could not be read: %w", err)
	}
	if status < 200 || status >= 300 {
		return SkillsSHResolution{}, true, fmt.Errorf("skills.sh repository tree lookup failed with HTTP status %d", status)
	}
	if tree.Truncated {
		return resolveSkillsSHArchive(ctx, client, owner, repository, slug, revision, fmt.Errorf("skills.sh repository tree is truncated"))
	}
	documents := make([]string, 0)
	seen := make(map[string]bool)
	skillDirectories := make(map[string]bool)
	for _, entry := range tree.Tree {
		if entry.Type != "blob" || !strings.EqualFold(path.Base(entry.Path), "SKILL.md") {
			continue
		}
		if !skillsSHSafePath(entry.Path) {
			return SkillsSHResolution{}, true, fmt.Errorf("skills.sh repository contains an unsafe skill path")
		}
		if skillsSHIgnoredPath(entry.Path) {
			continue
		}
		if (entry.Mode != "" && entry.Mode != "100644" && entry.Mode != "100755") || seen[entry.Path] {
			continue
		}
		seen[entry.Path] = true
		skillDirectories[strings.ToLower(path.Dir(entry.Path))] = true
		documents = append(documents, entry.Path)
	}
	// A nested example inside a non-root skill is supporting content. A root
	// entry may coexist with child catalog entries, as in the official blob
	// discovery path; do not hide those children merely because root SKILL.md exists.
	filtered := documents[:0]
	for _, document := range documents {
		ancestorSkill := false
		for dir := path.Dir(document); dir != "."; dir = path.Dir(dir) {
			parent := path.Dir(dir)
			if parent != "." && skillDirectories[strings.ToLower(parent)] {
				ancestorSkill = true
				break
			}
		}
		if !ancestorSkill {
			filtered = append(filtered, document)
		}
	}
	documents = filtered
	if len(documents) > skillsSHDiscoveryFiles {
		return resolveSkillsSHArchive(ctx, client, owner, repository, slug, revision, fmt.Errorf("skills.sh skill discovery exceeds %d HTTP documents", skillsSHDiscoveryFiles))
	}
	sort.Strings(documents)
	names := make([]string, len(documents))
	group, fetchCtx := errgroup.WithContext(ctx)
	group.SetLimit(6)
	for index, document := range documents {
		group.Go(func() error {
			name, readErr := reader.skillName(fetchCtx, owner, repository, revision, document)
			if readErr != nil {
				return fmt.Errorf("skills.sh skill discovery failed: %w", readErr)
			}
			names[index] = name
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return SkillsSHResolution{}, true, err
	}
	var selected SkillsSHResolution
	for index, document := range documents {
		name := names[index]
		if skillsSHSlug(name) != slug {
			continue
		}
		if selected.GitHubSourceURL != "" {
			return SkillsSHResolution{}, true, fmt.Errorf("skills.sh skill name is ambiguous; use an exact GitHub skill directory URL")
		}
		selected = skillsSHSelected(owner, repository, revision, skillsSHDirectory(document), name)
	}
	if selected.GitHubSourceURL != "" {
		return selected, true, nil
	}
	return SkillsSHResolution{}, true, fmt.Errorf("skills.sh skill %q was not found in %s/%s", slug, owner, repository)
}

func skillsSHHost(host string) bool {
	return strings.EqualFold(host, "skills.sh") || strings.EqualFold(host, "www.skills.sh")
}

// Matches the catalog's toSkillSlug, not a fuzzy substring or a directory guess.
func skillsSHSlug(name string) string {
	var slug strings.Builder
	separator := false
	for _, char := range strings.ToLower(name) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			if separator && slug.Len() > 0 {
				slug.WriteByte('-')
			}
			separator = false
			slug.WriteRune(char)
		case char == '-', char == '_', char == '\ufeff', unicode.IsSpace(char):
			separator = true
		}
	}
	return slug.String()
}

func skillsSHDirectory(document string) string {
	if dir := path.Dir(document); dir != "." {
		return dir
	}
	return ""
}

func skillsSHSelected(owner, repository, revision, directory, name string) SkillsSHResolution {
	parts := []string{owner, repository, "tree", revision}
	if directory != "" {
		parts = append(parts, strings.Split(directory, "/")...)
	}
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return SkillsSHResolution{GitHubSourceURL: "https://github.com/" + strings.Join(parts, "/"), CanonicalName: name}
}

func skillsSHJoinPath(directory, document string) string {
	if directory == "" {
		return document
	}
	return directory + "/" + document
}

func skillsSHSafePath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func skillsSHIgnoredPath(value string) bool {
	parts := strings.Split(value, "/")
	for _, part := range parts[:len(parts)-1] {
		switch part {
		case "node_modules", ".git", "dist", "build", "__pycache__":
			return true
		}
	}
	return false
}

func skillsSHPageSources(page []byte, owner, repository string) []*url.URL {
	tokenizer := html.NewTokenizer(bytes.NewReader(page))
	var sources []*url.URL
	seen := make(map[string]bool)
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			return sources
		}
		if tokenType != html.StartTagToken {
			continue
		}
		token := tokenizer.Token()
		if token.Data != "a" {
			continue
		}
		for _, attribute := range token.Attr {
			if attribute.Key != "href" {
				continue
			}
			parsed, err := url.Parse(attribute.Val)
			if err != nil || parsed.Scheme != "https" {
				continue
			}
			source, matched, err := parseGitHubSourceURL(parsed)
			if err != nil || !matched || !source.explicitDirectory || !strings.EqualFold(source.owner, owner) || !strings.EqualFold(source.repository, repository) {
				continue
			}
			parsed.RawQuery, parsed.Fragment = "", ""
			if seen[parsed.String()] {
				continue
			}
			seen[parsed.String()] = true
			sources = append(sources, parsed)
			if len(sources) > skillsSHPageSourceLinks {
				return nil
			}
		}
	}
}

type skillsSHReader struct {
	client *http.Client
	bytes  atomic.Int64
}

func (r *skillsSHReader) skillName(ctx context.Context, owner, repository, revision, document string) (string, error) {
	if r.bytes.Load() >= skillsSHDiscoveryBytes {
		return "", fmt.Errorf("skills.sh discovery exceeds its byte limit")
	}
	parts := []string{owner, repository, revision}
	parts = append(parts, strings.Split(document, "/")...)
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	body, err := r.get(ctx, "https://raw.githubusercontent.com/"+strings.Join(parts, "/"), skillsSHDocumentLimit, false)
	received := r.bytes.Add(int64(len(body)))
	if err != nil {
		return "", err
	}
	if received > skillsSHDiscoveryBytes {
		return "", fmt.Errorf("skills.sh discovery exceeds its byte limit")
	}
	name, complete := skillsSHFrontmatterName(body)
	if !complete {
		return "", fmt.Errorf("skills.sh skill frontmatter exceeds %d bytes", skillsSHDocumentLimit)
	}
	return name, nil
}

func skillsSHFrontmatterName(body []byte) (string, bool) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return "", true
	}
	for end := 1; end < len(lines); end++ {
		if lines[end] != "---" {
			continue
		}
		var metadata struct {
			Name        any `yaml:"name"`
			Description any `yaml:"description"`
		}
		if yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &metadata) != nil {
			return "", true
		}
		name, nameOK := metadata.Name.(string)
		description, descriptionOK := metadata.Description.(string)
		if !nameOK || !descriptionOK || strings.TrimSpace(description) == "" {
			return "", true
		}
		return strings.TrimSpace(name), true
	}
	return "", false
}

func (r *skillsSHReader) get(ctx context.Context, rawURL string, limit int64, page bool) ([]byte, error) {
	var hosts []string
	if page {
		hosts = []string{"skills.sh", "www.skills.sh"}
	} else {
		hosts = []string{"raw.githubusercontent.com"}
	}
	client := skillsSHHTTPClient(r.client, 15*time.Second, hosts...)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "LazyMind-skill-import")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("skills.sh source fetch failed with HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		if !page {
			if _, complete := skillsSHFrontmatterName(body[:limit]); complete {
				return body[:limit], nil
			}
		}
		return nil, fmt.Errorf("skills.sh source document exceeds %d bytes", limit)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return body, nil
}

func skillsSHHTTPClient(base *http.Client, timeout time.Duration, hosts ...string) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	if client.Timeout == 0 || client.Timeout > timeout {
		client.Timeout = timeout
	}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		allowed := false
		for _, host := range hosts {
			if strings.EqualFold(request.URL.Hostname(), host) {
				allowed = true
			}
		}
		if !allowed || request.URL.Scheme != "https" || request.URL.Port() != "" || request.URL.User != nil || len(via) >= 5 {
			return fmt.Errorf("skills.sh source redirect is not trusted")
		}
		request.Header.Del("Authorization")
		if base.CheckRedirect != nil {
			err := base.CheckRedirect(request, via)
			request.Header.Del("Authorization")
			return err
		}
		return nil
	}
	return &client
}

// The documented Git smart-HTTP advertisement supplies HEAD without consuming
// REST quota. Only the first bounded pkt-lines are needed; no Git subprocess,
// credentials, repository clone, or user-supplied endpoint is involved.
// https://git-scm.com/docs/http-protocol#_smart_clients
// https://git-scm.com/docs/pack-protocol#_reference_discovery
func skillsSHAdvertisedHead(ctx context.Context, client *http.Client, owner, repository string) (string, error) {
	endpoint := "https://github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) + ".git/info/refs?service=git-upload-pack"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "LazyMind-skill-import")
	response, err := skillsSHHTTPClient(client, 15*time.Second, "github.com").Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub reference discovery failed with HTTP status %d", response.StatusCode)
	}
	reader := io.LimitReader(response.Body, 1<<20)
	for {
		var prefix [4]byte
		if _, err := io.ReadFull(reader, prefix[:]); err != nil {
			return "", fmt.Errorf("GitHub HEAD could not be discovered: %w", err)
		}
		length, err := strconv.ParseUint(string(prefix[:]), 16, 16)
		if err != nil || (length > 0 && length < 4) {
			return "", fmt.Errorf("GitHub reference advertisement has an invalid packet")
		}
		if length == 0 {
			continue
		}
		packet := make([]byte, int(length)-4)
		if _, err := io.ReadFull(reader, packet); err != nil {
			return "", fmt.Errorf("GitHub reference advertisement is incomplete: %w", err)
		}
		ref, _, _ := strings.Cut(strings.TrimSuffix(string(packet), "\n"), "\x00")
		sha, name, found := strings.Cut(ref, " ")
		if found && name == "HEAD" && len(sha) == 40 && isHex(sha) && strings.Trim(sha, "0") != "" {
			return sha, nil
		}
	}
}

func resolveSkillsSHArchive(ctx context.Context, client *http.Client, owner, repository, slug, revision string, original error) (SkillsSHResolution, bool, error) {
	failed := func(err error) (SkillsSHResolution, bool, error) {
		return SkillsSHResolution{}, true, fmt.Errorf("%w; GitHub archive discovery failed: %v", original, err)
	}
	if revision == "" {
		var err error
		revision, err = skillsSHAdvertisedHead(ctx, client, owner, repository)
		if err != nil {
			return failed(err)
		}
	}
	endpoint := "https://codeload.github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) + "/zip/" + url.PathEscape(revision)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return failed(err)
	}
	request.Header.Set("User-Agent", "LazyMind-skill-import")
	response, err := skillsSHHTTPClient(client, 3*time.Minute, "codeload.github.com").Do(request)
	if err != nil {
		return failed(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return failed(fmt.Errorf("HTTP status %d", response.StatusCode))
	}
	if response.ContentLength > skillsSHArchiveLimit {
		return failed(fmt.Errorf("skill package download exceeds %d bytes", skillsSHArchiveLimit))
	}
	file, err := os.CreateTemp("", "lazymind-skills-discovery-*.zip")
	if err != nil {
		return failed(err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, err := io.Copy(file, io.LimitReader(response.Body, skillsSHArchiveLimit+1))
	if err != nil {
		return failed(err)
	}
	if size > skillsSHArchiveLimit {
		return failed(fmt.Errorf("skill package download exceeds %d bytes", skillsSHArchiveLimit))
	}
	reader, err := zip.NewReader(file, size)
	if err != nil {
		return failed(err)
	}
	documents := make(map[string]*zip.File)
	seen := make(map[string]bool)
	skillDirectories := make(map[string]bool)
	root := ""
	for _, entry := range reader.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if !skillsSHSafePath(name) {
			return failed(fmt.Errorf("unsafe archive path %q", name))
		}
		wrapper, relative, nested := strings.Cut(name, "/")
		if root == "" {
			root = wrapper
		}
		if root != wrapper || (!nested && !entry.FileInfo().IsDir()) {
			return failed(fmt.Errorf("archive must have one repository root"))
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if seen[relative] {
			return failed(fmt.Errorf("archive contains duplicate path %q", relative))
		}
		seen[relative] = true
		if !strings.EqualFold(path.Base(relative), "SKILL.md") || skillsSHIgnoredPath(relative) {
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return failed(fmt.Errorf("archive contains a symlink skill document"))
		}
		documents[relative] = entry
		skillDirectories[strings.ToLower(path.Dir(relative))] = true
	}
	paths := make([]string, 0, len(documents))
	for document := range documents {
		ancestorSkill := false
		for dir := path.Dir(document); dir != "."; dir = path.Dir(dir) {
			parent := path.Dir(dir)
			if parent != "." && skillDirectories[strings.ToLower(parent)] {
				ancestorSkill = true
				break
			}
		}
		if !ancestorSkill {
			paths = append(paths, document)
		}
	}
	if len(paths) > skillsSHArchiveFiles {
		return failed(fmt.Errorf("archive discovery exceeds %d skill documents", skillsSHArchiveFiles))
	}
	sort.Strings(paths)
	total := 0
	var selected SkillsSHResolution
	selectedDirectory := ""
	for _, document := range paths {
		if err := ctx.Err(); err != nil {
			return failed(err)
		}
		stream, err := documents[document].Open()
		if err != nil {
			return failed(err)
		}
		body, readErr := io.ReadAll(io.LimitReader(stream, skillsSHDocumentLimit+1))
		closeErr := stream.Close()
		if readErr != nil {
			return failed(readErr)
		}
		if closeErr != nil {
			return failed(closeErr)
		}
		if len(body) > skillsSHDocumentLimit {
			body = body[:skillsSHDocumentLimit]
		}
		total += len(body)
		if total > skillsSHArchiveBytes {
			return failed(fmt.Errorf("archive frontmatter discovery exceeds %d bytes", skillsSHArchiveBytes))
		}
		name, complete := skillsSHFrontmatterName(body)
		if !complete {
			return failed(fmt.Errorf("skill frontmatter exceeds %d bytes", skillsSHDocumentLimit))
		}
		if skillsSHSlug(name) != slug {
			continue
		}
		if selected.GitHubSourceURL != "" {
			return failed(fmt.Errorf("skills.sh skill name is ambiguous; use an exact GitHub skill directory URL"))
		}
		selectedDirectory = skillsSHDirectory(document)
		selected = skillsSHSelected(owner, repository, revision, selectedDirectory, name)
	}
	if selected.GitHubSourceURL == "" {
		return failed(fmt.Errorf("skills.sh skill %q was not found in %s/%s", slug, owner, repository))
	}
	if err := ctx.Err(); err != nil {
		return failed(err)
	}
	var pkg skillpackage.Package
	if selectedDirectory == "" {
		pkg, err = skillpackage.ReadZip(file.Name())
	} else {
		pkg, err = skillpackage.ReadZipSubdirectory(file.Name(), selectedDirectory)
	}
	if err != nil {
		return failed(err)
	}
	if err := ctx.Err(); err != nil {
		return failed(err)
	}
	if selectedDirectory == "" {
		pkg.PackageRoot = repository
	}
	selected.PreparedPackage = &PreparedPackage{Files: pkg.Files, PackageRoot: pkg.PackageRoot}
	return selected, true, nil
}
