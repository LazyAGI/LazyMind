package download

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
)

// fetchGit clones a git repository (shallow) and replaces git-lfs pointer
// files with their real content using the per-host resolve rule.
func fetchGit(ctx context.Context, u *url.URL, revision, dstDir string, progress ProgressFunc) ([]FetchedFile, error) {
	args := []string{"clone", "--depth", "1"}
	if rev := strings.TrimSpace(revision); rev != "" {
		args = append(args, "-b", rev)
	}
	args = append(args, u.String(), dstDir)
	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git clone %s failed: %w: %s", u, err, truncateBytes(out, 512))
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

// walkPackageFiles lists regular files under root, skipping the .git dir.
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
