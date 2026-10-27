package service

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"lazymind/core/skillv2/skillpackage"
)

const clawHubHandoffLimit = 1 << 20

// downloadClawHubSource accepts both hosted ZIPs and the public GitHub handoff
// documented by ClawHub's /api/v1/download. The descriptor's archiveUrl is never
// followed: only a validated repository, immutable commit, and path select bytes.
func (d HTTPZipDownloader) downloadClawHubSource(ctx context.Context, source SourceInput) (DownloadedZip, error) {
	if err := ctx.Err(); err != nil {
		return DownloadedZip{}, err
	}
	originalClient := d.httpClient()
	client := *originalClient
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	transfer := &githubTransferTransport{base: transport, cacheClient: originalClient}
	transfer.received.Store(source.ResolutionTransferBytes)
	client.Transport = transfer
	d.client = &client
	initial, err := d.Download(ctx, source.URL)
	if err != nil {
		return DownloadedZip{}, err
	}
	keepInitial := false
	defer func() {
		if !keepInitial && initial.Cleanup != nil {
			initial.Cleanup()
		}
	}()
	if archive, err := zip.OpenReader(initial.Path); err == nil {
		_ = archive.Close()
		if err := ctx.Err(); err != nil {
			return DownloadedZip{}, err
		}
		keepInitial = true
		return initial, nil
	}
	file, err := os.Open(initial.Path)
	if err != nil {
		return DownloadedZip{}, fmt.Errorf("ClawHub handoff could not be read: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(file, clawHubHandoffLimit+1))
	_ = file.Close()
	if err != nil {
		return DownloadedZip{}, fmt.Errorf("ClawHub handoff could not be read: %w", err)
	}
	if len(body) > clawHubHandoffLimit {
		return DownloadedZip{}, fmt.Errorf("ClawHub response is neither a ZIP nor a handoff within the 1 MiB limit")
	}
	if err := ctx.Err(); err != nil {
		return DownloadedZip{}, err
	}
	upstream, expectedHash, err := clawHubGitHubHandoff(body, source)
	if err != nil {
		return DownloadedZip{}, err
	}
	downloaded, err := d.DownloadSource(ctx, upstream)
	if err != nil {
		if downloaded.Cleanup != nil {
			downloaded.Cleanup()
		}
		return DownloadedZip{}, err
	}
	// A GitHub API/raw failure can fall back to a repository ZIP. Convert that
	// fallback to a standalone archive before returning to the ClawHub caller,
	// whose original SourceURL does not describe the GitHub archive wrapper.
	keepDownloaded := false
	defer func() {
		if !keepDownloaded && downloaded.Cleanup != nil {
			downloaded.Cleanup()
		}
	}()
	selection := ""
	if downloaded.PathPrefix == nil {
		selection = upstream.PathPrefix
	}
	files, err := clawHubOriginalSourceFiles(ctx, downloaded.Path, selection, downloaded.PathPrefix == nil)
	if err != nil {
		return DownloadedZip{}, fmt.Errorf("ClawHub GitHub package could not be selected: %w", err)
	}
	if clawHubFolderContentHash(files) != expectedHash {
		return DownloadedZip{}, fmt.Errorf("ClawHub GitHub folder content hash does not match the approved source")
	}
	if err := ctx.Err(); err != nil {
		return DownloadedZip{}, err
	}
	if downloaded.PathPrefix != nil {
		keepDownloaded = true
		return downloaded, nil
	}
	var pkg skillpackage.Package
	if upstream.PathPrefix == "" {
		pkg, err = skillpackage.ReadZip(downloaded.Path)
		if err == nil {
			err = skillpackage.NormalizeSkillDocument(pkg.Files)
		}
	} else {
		pkg, err = skillpackage.ReadGitHubZip(downloaded.Path, upstream.PathPrefix)
	}
	if err != nil {
		return DownloadedZip{}, fmt.Errorf("ClawHub GitHub package could not be selected: %w", err)
	}
	archivePath, err := skillpackage.WriteZip(pkg.Files, "")
	if err != nil {
		return DownloadedZip{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(archivePath)
		return DownloadedZip{}, err
	}
	empty := ""
	return DownloadedZip{
		Path: archivePath, Cleanup: func() { _ = os.Remove(archivePath) },
		PathPrefix: &empty, PackageRoot: path.Base(upstream.PathPrefix),
	}, nil
}

// Fingerprint the original names and bytes before the package reader normalizes
// SKILL.md casing or removes system metadata. GitHub repository archives have
// one wrapper directory; a standalone directory ZIP has none.
func clawHubOriginalSourceFiles(ctx context.Context, archivePath, selected string, repositoryArchive bool) (map[string][]byte, error) {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	wrapper := ""
	if repositoryArchive {
		for _, entry := range archive.File {
			if entry.FileInfo().IsDir() {
				continue
			}
			parts := strings.SplitN(entry.Name, "/", 2)
			if len(parts) != 2 || parts[0] == "" || (wrapper != "" && wrapper != parts[0]) {
				return nil, fmt.Errorf("GitHub archive does not have one repository wrapper")
			}
			wrapper = parts[0]
		}
	}
	files := make(map[string][]byte)
	var total uint64
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name, err := skillpackage.CleanPath(strings.TrimSuffix(entry.Name, "/"))
		if err != nil {
			return nil, err
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if repositoryArchive {
			name = strings.TrimPrefix(name, wrapper+"/")
		}
		if selected != "" {
			if !strings.HasPrefix(name, selected+"/") {
				continue
			}
			name = strings.TrimPrefix(name, selected+"/")
		}
		if _, err := skillpackage.CleanPath(name); err != nil {
			return nil, err
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("skill package cannot contain symlink %q", name)
		}
		if _, exists := files[name]; exists {
			return nil, fmt.Errorf("skill package contains duplicate path %q", name)
		}
		if len(files) >= skillpackage.MaxFiles || entry.UncompressedSize64 > skillpackage.MaxFileBytes {
			return nil, githubPackageTooLarge("ClawHub GitHub folder exceeds package limits")
		}
		total += entry.UncompressedSize64
		if total > skillpackage.MaxTotalBytes {
			return nil, githubPackageTooLarge("ClawHub GitHub folder exceeds unpacked size limit")
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, skillpackage.MaxFileBytes+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(data) > skillpackage.MaxFileBytes {
			return nil, githubPackageTooLarge("ClawHub GitHub folder file exceeds size limit")
		}
		files[name] = data
	}
	return files, nil
}

func clawHubGitHubHandoff(body []byte, original SourceInput) (SourceInput, string, error) {
	var descriptor struct {
		SourceRef   string `json:"sourceRef"`
		Repository  string `json:"repo"`
		Commit      string `json:"commit"`
		Path        string `json:"path"`
		ContentHash string `json:"contentHash"`
	}
	if json.Unmarshal(body, &descriptor) != nil || descriptor.SourceRef != "public-github" {
		return SourceInput{}, "", fmt.Errorf("ClawHub response must be a ZIP or a public-github source handoff")
	}
	parts := strings.Split(descriptor.Repository, "/")
	if len(parts) != 2 || !clawHubGitHubName(parts[0]) || !clawHubGitHubName(parts[1]) || !isGitHubCommit(descriptor.Commit) || !safeGitHubPackagePath(descriptor.Path) {
		return SourceInput{}, "", fmt.Errorf("ClawHub GitHub handoff requires a valid repository, pinned 40-character commit, and skill directory")
	}
	if len(descriptor.ContentHash) != 64 {
		return SourceInput{}, "", fmt.Errorf("ClawHub GitHub handoff is missing a valid content hash")
	}
	if _, err := hex.DecodeString(descriptor.ContentHash); err != nil {
		return SourceInput{}, "", fmt.Errorf("ClawHub GitHub handoff has an invalid content hash")
	}
	commit := strings.ToLower(descriptor.Commit)
	repoURL := "https://github.com/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
	sourceURL := repoURL + "/tree/" + commit
	for _, segment := range strings.Split(descriptor.Path, "/") {
		sourceURL += "/" + url.PathEscape(segment)
	}
	resolved := original
	resolved.URL = repoURL + "/archive/" + commit + ".zip"
	resolved.ResolvedSourceURL = sourceURL
	resolved.PathPrefix = descriptor.Path
	resolved.PathPrefixResolved = true
	return resolved, strings.ToLower(descriptor.ContentHash), nil
}

// ClawHub's folder fingerprint includes names, sizes and file hashes, ordered
// using the English collation used by JavaScript's default localeCompare.
// This is distinct from both a ZIP hash and LazyMind's package tree hash.
func clawHubFolderContentHash(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for name := range files {
		paths = append(paths, name)
	}
	collate.New(language.English).SortStrings(paths)
	lines := make([]string, 0, len(paths))
	for _, name := range paths {
		contentHash := sha256.Sum256(files[name])
		lines = append(lines, fmt.Sprintf("%s\x00%d\x00%x", name, len(files[name]), contentHash))
	}
	result := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(result[:])
}

func clawHubGitHubName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, char := range name {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return true
}
