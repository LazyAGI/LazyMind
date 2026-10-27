package desktopupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func excludedSnapshotPath(rel string, excludes []string) bool {
	for _, p := range excludes {
		if rel == p || strings.HasPrefix(rel, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// The walker never follows links. Only regular files, directories and opaque
// symbolic links belong to the v1 data snapshot contract.
func walkSnapshot(ctx context.Context, root string, excludes []string, visit func(string, string, os.FileInfo) error) error {
	var walk func(string, string) error
	walk = func(path, rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if rel != "." && excludedSnapshotPath(rel, excludes) {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return failure("snapshot_type", nil)
		}
		if info.Mode().IsRegular() {
			linked, err := hasMultipleLinks(path, info)
			if err != nil {
				return err
			}
			if linked {
				return failure("snapshot_hardlink", nil)
			}
		}
		if err = visit(path, rel, info); err != nil {
			return err
		}
		if info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err = walk(filepath.Join(path, entry.Name()), filepath.Join(rel, entry.Name())); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(root, ".")
}

func treeDigest(ctx context.Context, root string, excludes []string) (string, bool, error) {
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return digestBytes([]byte("absent/v1")), false, nil
	} else if err != nil {
		return "", false, err
	}
	hash := sha256.New()
	err := walkSnapshot(ctx, root, excludes, func(path, rel string, info os.FileInfo) error {
		header := struct {
			Path   string
			Mode   uint32
			Size   int64
			Target string
		}{Path: filepath.ToSlash(rel), Mode: uint32(info.Mode())}
		if info.Mode().IsRegular() {
			header.Size = info.Size()
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			header.Target = target
		}
		raw, err := json.Marshal(header)
		if err != nil {
			return err
		}
		_, _ = hash.Write(raw)
		_, _ = hash.Write([]byte{'\n'})
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			n, err := io.Copy(hash, contextReader{ctx, f})
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			if n != info.Size() {
				return failure("snapshot_changed", nil)
			}
		}
		return nil
	})
	if err != nil {
		return "", true, err
	}
	return hex.EncodeToString(hash.Sum(nil)), true, nil
}

func copySnapshot(ctx context.Context, source, destination string, excludes []string) error {
	if _, err := os.Lstat(source); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return failure("snapshot_exists", err)
	}
	var directories []struct {
		path string
		info os.FileInfo
	}
	err := walkSnapshot(ctx, source, excludes, func(path, rel string, info os.FileInfo) error {
		target := destination
		if rel != "." {
			target = filepath.Join(destination, rel)
		}
		if info.IsDir() {
			if err := os.Mkdir(target, 0700); err != nil {
				return err
			}
			directories = append(directories, struct {
				path string
				info os.FileInfo
			}{target, info})
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = io.Copy(output, contextReader{ctx, input})
		if err == nil {
			err = output.Chmod(info.Mode().Perm())
		}
		if err == nil {
			err = output.Sync()
		}
		closeErr := output.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		return os.Chtimes(target, info.ModTime(), info.ModTime())
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		entry := directories[i]
		if err = syncDirectory(entry.path); err != nil {
			return err
		}
		if err = os.Chmod(entry.path, entry.info.Mode().Perm()); err != nil {
			return err
		}
		if err = os.Chtimes(entry.path, entry.info.ModTime(), entry.info.ModTime()); err != nil {
			return err
		}
	}
	return syncDirectory(filepath.Dir(destination))
}

func reserveSpace(ctx context.Context, path string, size int64) error {
	available, err := freeDiskBytes(filepath.Dir(path))
	if err != nil {
		return failure("space_unknown", err)
	}
	if uint64(size) > available {
		return failure("space_insufficient", nil)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	buffer := make([]byte, 1024*1024)
	remaining := size
	for remaining > 0 && err == nil {
		if err = ctx.Err(); err != nil {
			break
		}
		count := int64(len(buffer))
		if count > remaining {
			count = remaining
		}
		var n int
		n, err = f.Write(buffer[:count])
		remaining -= int64(n)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return failure("space_reservation", err)
	}
	return syncDirectory(filepath.Dir(path))
}
