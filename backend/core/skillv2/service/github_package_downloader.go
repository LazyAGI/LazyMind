package service

import (
	"archive/zip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"lazymind/core/skillv2/httperr"
	"lazymind/core/skillv2/skillpackage"
	"lazymind/core/skillv2/sourceurl"
)

const githubRawBaseURL = "https://raw.githubusercontent.com"

type githubTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

type githubTree struct {
	SHA       string            `json:"sha"`
	Truncated bool              `json:"truncated"`
	Entries   []githubTreeEntry `json:"tree"`
}

type githubRetrievalError struct{ err error }

func (e *githubRetrievalError) Error() string { return e.err.Error() }
func (e *githubRetrievalError) Unwrap() error { return e.err }

// DownloadSource first assembles the selected GitHub directory. Archive fallback
// handles unavailable tree/raw endpoints; a known invalid package is never
// retried as a whole repository.
func (d HTTPZipDownloader) DownloadSource(ctx context.Context, source SourceInput) (DownloadedZip, error) {
	if err := ctx.Err(); err != nil {
		return DownloadedZip{}, err
	}
	if source.ResolutionTransferBytes > MaxSkillDownloadBytes {
		return DownloadedZip{}, githubPackageTooLarge("skill package download exceeds transfer limit")
	}
	if prepared := source.PreparedPackage; prepared != nil {
		archive, err := skillpackage.WriteZip(prepared.Files, "")
		if err != nil {
			return DownloadedZip{}, err
		}
		cleanup := func() { _ = os.Remove(archive) }
		if err := ctx.Err(); err != nil {
			cleanup()
			return DownloadedZip{}, err
		}
		empty := ""
		return DownloadedZip{Path: archive, Cleanup: cleanup, PathPrefix: &empty, PackageRoot: prepared.PackageRoot}, nil
	}
	if download, err := url.Parse(source.URL); err == nil && strings.EqualFold(strings.TrimPrefix(strings.ToLower(download.Hostname()), "www."), "clawhub.ai") && download.Path == "/api/v1/download" {
		return d.downloadClawHubSource(ctx, source)
	}
	original := strings.TrimSpace(source.ResolvedSourceURL)
	if original == "" {
		original = strings.TrimSpace(source.SourceURL)
	}
	if original == "" {
		original = source.URL
	}
	parsed, err := url.Parse(original)
	if err != nil {
		return DownloadedZip{}, err
	}
	resolution, matched, err := sourceurl.ResolveGitHubPageURLFromResolvedArchive(parsed, source.URL)
	if err != nil {
		return DownloadedZip{}, err
	}
	if !matched {
		return d.Download(ctx, source.URL)
	}
	originalClient := d.httpClient()
	client := *originalClient
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	transfer := &githubTransferTransport{base: base, cacheClient: originalClient}
	transfer.received.Store(source.ResolutionTransferBytes)
	client.Transport = transfer
	downloaded, err := d.downloadGitHubDirectory(ctx, &client, source, resolution)
	if err == nil {
		return downloaded, nil
	}
	if ctx.Err() != nil {
		return DownloadedZip{}, ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return DownloadedZip{}, err
	}
	var retrieval *githubRetrievalError
	if !errors.As(err, &retrieval) {
		return DownloadedZip{}, err
	}
	d.client = &client
	return d.Download(ctx, source.URL)
}

func (d HTTPZipDownloader) downloadGitHubDirectory(ctx context.Context, client *http.Client, source SourceInput, resolution sourceurl.GitHubResolution) (DownloadedZip, error) {
	apiBase := strings.TrimRight(d.githubAPIBaseURL, "/")
	if apiBase == "" {
		apiBase = sourceurl.GitHubAPIBaseURL
	}
	repoAPI := apiBase + "/repos/" + url.PathEscape(resolution.Owner) + "/" + url.PathEscape(resolution.Repository)
	revision := resolution.Revision
	if !isGitHubCommit(revision) {
		var commit struct {
			SHA string `json:"sha"`
		}
		if err := githubPackageAPIGet(ctx, client, repoAPI+"/commits/"+url.PathEscape(revision), &commit); err != nil {
			return DownloadedZip{}, err
		}
		if !isGitHubCommit(commit.SHA) {
			return DownloadedZip{}, githubRetrieve("GitHub commit lookup returned an unsupported revision")
		}
		revision = commit.SHA
	}
	getTree := func(sha string, recursive bool) (githubTree, error) {
		endpoint := repoAPI + "/git/trees/" + url.PathEscape(sha)
		if recursive {
			endpoint += "?recursive=1"
		}
		var tree githubTree
		if err := githubPackageAPIGet(ctx, client, endpoint, &tree); err != nil {
			return githubTree{}, err
		}
		if tree.Truncated {
			return githubTree{}, githubRetrieve("GitHub tree listing was truncated")
		}
		if tree.SHA == "" {
			return githubTree{}, githubRetrieve("GitHub tree listing is unsupported")
		}
		return tree, nil
	}
	selectedPath := source.PathPrefix
	if selectedPath == "" && !source.PathPrefixResolved {
		selectedPath = resolution.PathPrefix
	}
	var entries []githubTreeEntry
	if selectedPath == "" {
		tree, err := getTree(revision, true)
		if err != nil {
			return DownloadedZip{}, err
		}
		if resolution.ExplicitDirectory {
			entries = tree.Entries
		} else {
			var skills []string
			for _, entry := range tree.Entries {
				if entry.Type == "blob" && strings.EqualFold(path.Base(entry.Path), "SKILL.md") {
					skills = append(skills, entry.Path)
				}
			}
			if len(skills) == 0 {
				return DownloadedZip{}, githubMissingSkill("")
			}
			if len(skills) > 1 {
				return DownloadedZip{}, githubMultipleSkills()
			}
			if !safeGitHubPackagePath(skills[0]) {
				return DownloadedZip{}, githubUnsafePath(skills[0])
			}
			selectedPath = path.Dir(skills[0])
			if selectedPath == "." {
				selectedPath = ""
			}
			entries = githubEntriesWithin(tree.Entries, selectedPath)
		}
	} else {
		if !safeGitHubPackagePath(selectedPath) {
			return DownloadedZip{}, githubUnsafePath(selectedPath)
		}
		candidates := []string{selectedPath}
		if source.PathPrefix == "" && !source.PathPrefixResolved && len(resolution.PathPrefixCandidates) > 0 {
			candidates = resolution.PathPrefixCandidates
		}
		root, err := getTree(revision, false)
		if err != nil {
			return DownloadedZip{}, err
		}
		trees := map[string]githubTree{revision: root}
		found := false
		for _, candidate := range candidates {
			if !safeGitHubPackagePath(candidate) {
				return DownloadedZip{}, githubUnsafePath(candidate)
			}
			sha := revision
			exists := true
			for _, part := range strings.Split(candidate, "/") {
				tree, ok := trees[sha]
				if !ok {
					var err error
					tree, err = getTree(sha, false)
					if err != nil {
						return DownloadedZip{}, err
					}
					trees[sha] = tree
				}
				entry, ok := githubDirectEntry(tree.Entries, part)
				if !ok {
					exists = false
					break
				}
				if err := validateGitHubEntry(entry); err != nil {
					return DownloadedZip{}, err
				}
				if entry.Type != "tree" {
					exists = false
					break
				}
				if entry.SHA == "" {
					return DownloadedZip{}, githubRetrieve("GitHub directory tree has no SHA")
				}
				sha = entry.SHA
			}
			if !exists {
				continue
			}
			subtree, err := getTree(sha, true)
			if err != nil {
				return DownloadedZip{}, err
			}
			if !githubHasRootSkill(subtree.Entries) {
				continue
			}
			if found {
				return DownloadedZip{}, githubMultipleSkills()
			}
			selectedPath, entries, found = candidate, subtree.Entries, true
		}
		if !found {
			return DownloadedZip{}, githubMissingSkill(selectedPath)
		}
	}
	files, err := githubPackageFiles(entries)
	if err != nil {
		return DownloadedZip{}, err
	}
	rootName := path.Base(selectedPath)
	if selectedPath == "" {
		rootName = resolution.Repository
	}
	return d.writeGitHubPackage(ctx, client, resolution.Owner, resolution.Repository, revision, selectedPath, rootName, files)
}

func githubPackageAPIGet(ctx context.Context, client *http.Client, endpoint string, out any) error {
	status, err := sourceurl.GitHubAPIGet(ctx, client, endpoint, out)
	if err != nil {
		var transfer *githubTransferLimitError
		if errors.As(err, &transfer) {
			return githubPackageTooLarge("skill package download exceeds transfer limit")
		}
		return &githubRetrievalError{err: err}
	}
	if status < 200 || status >= 300 {
		return githubRetrieve(fmt.Sprintf("GitHub tree lookup failed with HTTP status %d", status))
	}
	return nil
}
func githubRetrieve(message string) error { return &githubRetrievalError{err: errors.New(message)} }
func githubPackageTooLarge(message string) error {
	return httperr.New(http.StatusRequestEntityTooLarge, httperr.CodePayloadTooLarge, message)
}
func githubUnsafePath(name string) error {
	return httperr.New(http.StatusBadRequest, httperr.CodeInvalidPath, fmt.Sprintf("unsafe path %q", name))
}
func githubMissingSkill(selected string) error {
	message := "skill package must contain SKILL.md"
	if selected != "" {
		message += fmt.Sprintf(" at the root of GitHub directory %q", selected)
	}
	return httperr.New(http.StatusUnprocessableEntity, httperr.CodeSkillMDNotFound, message)
}
func githubMultipleSkills() error {
	return httperr.New(http.StatusBadRequest, httperr.CodeGitHubMultipleSkills, "GitHub repository contains multiple skills; provide a specific skill directory or SKILL.md URL")
}
func isGitHubCommit(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	_, err := hex.DecodeString(ref)
	return err == nil
}
func safeGitHubPackagePath(name string) bool {
	if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
func githubDirectEntry(entries []githubTreeEntry, name string) (githubTreeEntry, bool) {
	for _, entry := range entries {
		if entry.Path == name {
			return entry, true
		}
	}
	return githubTreeEntry{}, false
}
func githubHasRootSkill(entries []githubTreeEntry) bool {
	for _, entry := range entries {
		if !strings.Contains(entry.Path, "/") && strings.EqualFold(entry.Path, "SKILL.md") {
			return true
		}
	}
	return false
}
func githubEntriesWithin(entries []githubTreeEntry, prefix string) []githubTreeEntry {
	if prefix == "" {
		return entries
	}
	var selected []githubTreeEntry
	for _, entry := range entries {
		if strings.HasPrefix(entry.Path, prefix+"/") {
			entry.Path = strings.TrimPrefix(entry.Path, prefix+"/")
			selected = append(selected, entry)
		}
	}
	return selected
}
func validateGitHubEntry(entry githubTreeEntry) error {
	if !safeGitHubPackagePath(entry.Path) {
		return githubUnsafePath(entry.Path)
	}
	switch {
	case entry.Mode == "120000":
		return httperr.New(http.StatusBadRequest, httperr.CodeSkillPackageInvalid, fmt.Sprintf("skill package cannot contain symlink %q", entry.Path))
	case entry.Mode == "160000" || entry.Type == "commit":
		return httperr.New(http.StatusBadRequest, httperr.CodeSkillPackageInvalid, fmt.Sprintf("skill package cannot contain submodule %q", entry.Path))
	case entry.Type == "tree" && entry.Mode == "040000":
		return nil
	case entry.Type == "blob" && (entry.Mode == "100644" || entry.Mode == "100755"):
		return nil
	default:
		return githubRetrieve("GitHub tree contains an unsupported file type")
	}
}
func githubPackageFiles(entries []githubTreeEntry) ([]githubTreeEntry, error) {
	var files []githubTreeEntry
	var total int64
	seen := map[string]bool{}
	rootSkills := 0
	filePaths := map[string]bool{}
	for _, entry := range entries {
		if err := validateGitHubEntry(entry); err != nil {
			return nil, err
		}
		if seen[entry.Path] {
			return nil, githubUnsafePath(entry.Path)
		}
		seen[entry.Path] = true
		if entry.Type == "tree" {
			continue
		}
		if entry.Size < 0 {
			return nil, githubRetrieve("GitHub file has an unsupported size")
		}
		if entry.Size > skillpackage.MaxFileBytes {
			return nil, githubPackageTooLarge(fmt.Sprintf("skill package file %q exceeds %d bytes", entry.Path, skillpackage.MaxFileBytes))
		}
		total += entry.Size
		if total > skillpackage.MaxTotalBytes {
			return nil, githubPackageTooLarge("skill package exceeds unpacked size limit")
		}
		if total > MaxSkillDownloadBytes {
			return nil, githubPackageTooLarge("skill package download exceeds transfer limit")
		}
		files = append(files, entry)
		filePaths[entry.Path] = true
		if len(files) > skillpackage.MaxFiles {
			return nil, githubPackageTooLarge("skill package contains too many entries")
		}
		if strings.EqualFold(entry.Path, "SKILL.md") {
			rootSkills++
		}
	}
	if rootSkills == 0 {
		return nil, githubMissingSkill("")
	}
	if rootSkills > 1 {
		return nil, httperr.New(http.StatusUnprocessableEntity, httperr.CodeSkillMDAmbiguous, "skill package has ambiguous SKILL.md paths")
	}
	// Reject files whose parent is also a file, before making any raw request.
	for _, entry := range files {
		for parent := path.Dir(entry.Path); parent != "."; parent = path.Dir(parent) {
			if filePaths[parent] {
				return nil, githubUnsafePath(entry.Path)
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func (d HTTPZipDownloader) writeGitHubPackage(ctx context.Context, client *http.Client, owner, repo, revision, selected, rootName string, files []githubTreeEntry) (DownloadedZip, error) {
	output, err := os.CreateTemp("", "lazymind-skill-directory-*.zip")
	if err != nil {
		return DownloadedZip{}, err
	}
	cleanup := func() { _ = os.Remove(output.Name()) }
	writer := zip.NewWriter(output)
	failed := true
	defer func() {
		if failed {
			_ = writer.Close()
			_ = output.Close()
			cleanup()
		}
	}()
	directory, err := os.MkdirTemp("", "lazymind-skill-files-*")
	if err != nil {
		return DownloadedZip{}, err
	}
	defer os.RemoveAll(directory)
	rawBase := strings.TrimRight(d.githubRawBaseURL, "/")
	if rawBase == "" {
		rawBase = githubRawBaseURL
	}
	paths := make([]string, len(files))
	sizes := make([]int64, len(files))
	digests := make([]string, len(files))
	var unpacked atomic.Int64
	group, fetchCtx := errgroup.WithContext(ctx)
	group.SetLimit(6)
	for index, file := range files {
		group.Go(func() error {
			if err := fetchCtx.Err(); err != nil {
				return err
			}
			filename := file.Path
			if selected != "" {
				filename = selected + "/" + filename
			}
			segments := append([]string{owner, repo, revision}, strings.Split(filename, "/")...)
			for i := range segments {
				segments[i] = url.PathEscape(segments[i])
			}
			request, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, rawBase+"/"+strings.Join(segments, "/"), nil)
			if err != nil {
				return err
			}
			response, err := client.Do(request)
			if err != nil {
				return githubRawReadError(err)
			}
			defer response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				return githubRetrieve(fmt.Sprintf("GitHub file download failed with HTTP status %d", response.StatusCode))
			}
			if response.ContentLength > skillpackage.MaxFileBytes {
				return githubPackageTooLarge(fmt.Sprintf("skill package file %q exceeds %d bytes", file.Path, skillpackage.MaxFileBytes))
			}
			part, err := os.CreateTemp(directory, "part-*")
			if err != nil {
				return err
			}
			paths[index] = part.Name()
			// Git object IDs hash the blob header and bytes, not the bytes alone.
			// Verify real Git SHAs while retaining opaque IDs used by custom
			// providers and fixtures that do not expose content-addressed blobs.
			blobHash := sha1.New()
			destination := io.Writer(part)
			verifySHA := isGitHubCommit(file.SHA)
			if verifySHA {
				_, _ = fmt.Fprintf(blobHash, "blob %d\x00", file.Size)
				destination = io.MultiWriter(part, blobHash)
			}
			written, copyErr := io.Copy(destination, io.LimitReader(response.Body, skillpackage.MaxFileBytes+1))
			if verifySHA {
				digests[index] = hex.EncodeToString(blobHash.Sum(nil))
			}
			closeErr := part.Close()
			sizes[index] = written
			if written > skillpackage.MaxFileBytes {
				return githubPackageTooLarge(fmt.Sprintf("skill package file %q exceeds %d bytes", file.Path, skillpackage.MaxFileBytes))
			}
			if unpacked.Add(written) > skillpackage.MaxTotalBytes {
				return githubPackageTooLarge("skill package exceeds unpacked size limit")
			}
			if copyErr != nil {
				return githubRawReadError(copyErr)
			}
			return closeErr
		})
	}
	if err := group.Wait(); err != nil {
		return DownloadedZip{}, err
	}
	if err := ctx.Err(); err != nil {
		return DownloadedZip{}, err
	}
	// Validate complete responses before writing anything into the final ZIP.
	// Checking after all workers preserves the combined streaming size bound.
	for index, file := range files {
		if sizes[index] != file.Size {
			return DownloadedZip{}, githubRetrieve(fmt.Sprintf("GitHub file %q length does not match its tree entry: received %d, expected %d", file.Path, sizes[index], file.Size))
		}
		if digests[index] != "" && !strings.EqualFold(digests[index], file.SHA) {
			return DownloadedZip{}, githubRetrieve(fmt.Sprintf("GitHub file %q content does not match its tree blob SHA", file.Path))
		}
	}
	for index, file := range files {
		if err := ctx.Err(); err != nil {
			return DownloadedZip{}, err
		}
		header := &zip.FileHeader{Name: file.Path, Method: zip.Deflate}
		mode := os.FileMode(0644)
		if file.Mode == "100755" {
			mode = 0755
		}
		header.SetMode(mode)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return DownloadedZip{}, err
		}
		part, err := os.Open(paths[index])
		if err != nil {
			return DownloadedZip{}, err
		}
		_, copyErr := io.Copy(entry, part)
		closeErr := part.Close()
		if copyErr != nil {
			return DownloadedZip{}, copyErr
		}
		if closeErr != nil {
			return DownloadedZip{}, closeErr
		}
	}
	if err := writer.Close(); err != nil {
		return DownloadedZip{}, err
	}
	if err := output.Close(); err != nil {
		return DownloadedZip{}, err
	}
	failed = false
	empty := ""
	return DownloadedZip{Path: output.Name(), Cleanup: cleanup, PathPrefix: &empty, PackageRoot: rootName}, nil
}

func githubRawReadError(err error) error {
	var transfer *githubTransferLimitError
	if errors.As(err, &transfer) {
		return githubPackageTooLarge("skill package download exceeds transfer limit")
	}
	return &githubRetrievalError{err: err}
}

// Count bytes across API metadata and all raw responses, even when a server
// omits Content-Length or understates a file's size in its tree response.
type githubTransferTransport struct {
	base        http.RoundTripper
	cacheClient *http.Client
	received    atomic.Int64
}

func (t *githubTransferTransport) GitHubAPICacheClient() *http.Client { return t.cacheClient }

type githubTransferLimitError struct{}

func (*githubTransferLimitError) Error() string             { return "GitHub package transfer limit exceeded" }
func (*githubTransferLimitError) GitHubTransferLimit() bool { return true }
func (t *githubTransferTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.ContentLength > MaxSkillDownloadBytes-t.received.Load() {
		_ = response.Body.Close()
		return nil, &githubTransferLimitError{}
	}
	response.Body = &githubTransferBody{ReadCloser: response.Body, total: &t.received}
	return response, nil
}

type githubTransferBody struct {
	io.ReadCloser
	total *atomic.Int64
}

func (b *githubTransferBody) Read(p []byte) (int, error) {
	remaining := MaxSkillDownloadBytes - b.total.Load()
	if remaining < 0 {
		return 0, &githubTransferLimitError{}
	}
	if int64(len(p)) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	if b.total.Add(int64(n)) > MaxSkillDownloadBytes {
		return n, &githubTransferLimitError{}
	}
	return n, err
}
