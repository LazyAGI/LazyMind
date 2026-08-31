package download

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// fetchGit clones a git repository (shallow) and replaces git-lfs pointer
// files with their real content using the per-host resolve rule.
func fetchGit(ctx context.Context, u *url.URL, revision, dstDir string, progress ProgressFunc) ([]FetchedFile, error) {
	args := []string{"clone", "--depth", "1", "--progress"}
	if rev := strings.TrimSpace(revision); rev != "" {
		args = append(args, "-b", rev)
	}
	args = append(args, u.String(), dstDir)

	cmd := exec.CommandContext(ctx, "git", args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("git clone stderr pipe failed: %w", err)
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git clone start failed: %w", err)
	}

	stderrLog, err := scanGitCloneStderr(stderr, progress)
	if err != nil {
		return nil, err
	}
	waitErr := cmd.Wait()
	if waitErr != nil {
		return nil, fmt.Errorf("git clone %s failed: %w: %s", u, waitErr, truncateBytes([]byte(stderrLog), 512))
	}

	paths, err := walkPackageFiles(dstDir)
	if err != nil {
		return nil, err
	}
	for _, rel := range paths {
		full := filepath.Join(dstDir, rel)
		if isLFSPointerFile(full) {
			if err := resolveLFSPointer(ctx, u, revision, dstDir, rel); err != nil {
				return nil, err
			}
		}
	}

	if err := extractNestedZips(dstDir); err != nil {
		return nil, err
	}
	paths, err = walkPackageFiles(dstDir)
	if err != nil {
		return nil, err
	}

	files := make([]FetchedFile, 0, len(paths))
	for _, rel := range paths {
		full := filepath.Join(dstDir, rel)
		size, sha, err := hashFile(full)
		if err != nil {
			return nil, fmt.Errorf("hash %s failed: %w", rel, err)
		}
		files = append(files, FetchedFile{Path: rel, Size: size, SHA256: sha})
	}
	return files, nil
}

// scanGitCloneStderr consumes git clone stderr, records the raw text for
// error reporting, and maps progress lines onto the optional callback.
func scanGitCloneStderr(stderr io.Reader, progress ProgressFunc) (string, error) {
	var stderrLog strings.Builder
	scanner := bufio.NewScanner(stderr)
	scanner.Split(scanGitProgressLines)
	for scanner.Scan() {
		line := scanner.Text()
		stderrLog.WriteString(line)
		stderrLog.WriteByte('\n')
		if percent, ok := gitClonePercent(line); ok && progress != nil {
			progress(int64(percent), 100)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read git clone progress failed: %w", err)
	}
	return stderrLog.String(), nil
}

// scanGitProgressLines splits stderr on either '\n' or '\r'. git clone
// --progress on a pipe overwrites the same line with '\r'-separated updates
// and rarely emits '\n' during long transfers, so the default newline-only
// split can accumulate a single token past bufio.Scanner's 64KB limit and
// abort the whole download (observed on slow, multi-GB clones). Splitting on
// '\r' keeps every progress update its own small token.
func scanGitProgressLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' || b == '\r' {
			if i > 0 {
				return i + 1, data[:i], nil
			}
			return 1, nil, nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// gitClonePercent maps a git clone stderr progress line onto a monotonic
// 0-99 percentage. Receiving objects dominates clone time, followed by
// resolving deltas and checking out files.
func gitClonePercent(line string) (int, bool) {
	line = strings.TrimSpace(line)
	var base, span int
	switch {
	case strings.Contains(line, "Receiving objects"):
		base, span = 0, 80
	case strings.Contains(line, "Resolving deltas"):
		base, span = 80, 15
	case strings.Contains(line, "Checking out files"):
		base, span = 95, 5
	default:
		return 0, false
	}

	raw, ok := percentFromLine(line)
	if !ok {
		return 0, false
	}
	scaled := base + span*raw/100
	if scaled > 99 {
		scaled = 99
	}
	return scaled, true
}

// percentFromLine extracts the first integer percentage immediately before a
// '%' character.
func percentFromLine(line string) (int, bool) {
	idx := strings.IndexByte(line, '%')
	if idx <= 0 {
		return 0, false
	}
	start := idx - 1
	for start >= 0 && line[start] >= '0' && line[start] <= '9' {
		start--
	}
	start++
	if start == idx {
		return 0, false
	}
	n, err := strconv.Atoi(line[start:idx])
	if err != nil || n < 0 || n > 100 {
		return 0, false
	}
	return n, true
}

// walkPackageFiles lists regular files under root, skipping the .git dir.

// extractNestedZips expands every zip archive found inside a cloned package so
// packaged corpora (e.g. UDA-QA source-document zips) become directly
// importable. Extraction is iterative with a depth cap to bound zip-in-zip
// nesting; archives are removed after a successful extraction.
func extractNestedZips(root string) error {
	const maxDepth = 4
	for depth := 0; depth < maxDepth; depth++ {
		zips, err := findZips(root)
		if err != nil {
			return err
		}
		if len(zips) == 0 {
			return nil
		}
		for _, zipPath := range zips {
			if err := extractZip(zipPath, root); err != nil {
				return fmt.Errorf("extract nested zip %s failed: %w", zipPath, err)
			}
			if err := os.Remove(zipPath); err != nil {
				return fmt.Errorf("remove nested zip %s failed: %w", zipPath, err)
			}
		}
	}
	return fmt.Errorf("nested zip extraction exceeded depth %d", maxDepth)
}

func findZips(root string) ([]string, error) {
	var zips []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".zip") {
			zips = append(zips, path)
		}
		return nil
	})
	return zips, err
}

func walkPackageFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	return paths, err
}

// RemoteRevision returns the remote commit hash of the pinned branch/tag (or
// the default HEAD when revision is empty) of a git package URL. It is the
// update check baseline for git-sourced knowledge bases: the installed
// config.commit is compared against it to decide whether an update is needed.
func RemoteRevision(ctx context.Context, packageURL, revision string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(packageURL))
	if err != nil {
		return "", fmt.Errorf("invalid package url: %w", err)
	}
	if !strings.HasSuffix(strings.ToLower(u.Path), ".git") {
		return "", fmt.Errorf("not a git repository url: %s", packageURL)
	}
	ref := strings.TrimSpace(revision)
	args := []string{"ls-remote", u.String()}
	if ref == "" {
		args = append(args, "HEAD")
	} else {
		args = append(args, "refs/heads/"+ref, "refs/tags/"+ref)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s failed: %w", u, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 1 && len(fields[0]) == 40 {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("git ls-remote %s returned no commit", u)
}

// LocalCommit returns the checked-out commit hash of a local git working tree.
// It is persisted into config.commit after a successful install/update so the
// next update can diff against the remote revision.
func LocalCommit(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD failed: %w", err)
	}
	commit := strings.TrimSpace(string(out))
	if len(commit) != 40 {
		return "", fmt.Errorf("git rev-parse HEAD returned invalid commit %q", commit)
	}
	return commit, nil
}

// truncateBytes keeps the head of a command output for error messages.

func truncateBytes(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}
