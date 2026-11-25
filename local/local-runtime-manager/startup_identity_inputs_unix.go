//go:build darwin || linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// The helper performs the actual deep verification. Only the batch owner
// walks the bundle to detect changes across that verification, including
// in-place edits to nested signed resources whose parent directory is unchanged.
func snapshotStartupIdentityInputs(ctx context.Context, paths RuntimePaths) ([32]byte, error) {
	var sum [32]byte
	resources := filepath.Dir(paths.ResourcesRoot)
	contents := filepath.Dir(resources)
	bundle := filepath.Dir(contents)
	if !filepath.IsAbs(bundle) || filepath.Ext(bundle) != ".app" ||
		filepath.Base(contents) != "Contents" || filepath.Base(resources) != "Resources" {
		return sum, fs.ErrInvalid
	}
	helperDir := filepath.Join(paths.ResourcesRoot, "bin")
	for _, dir := range []string{bundle, contents, resources, paths.ResourcesRoot, helperDir} {
		info, err := os.Lstat(dir)
		if err != nil {
			return sum, err
		}
		if !info.IsDir() {
			return sum, fs.ErrInvalid
		}
	}
	helper, err := os.Lstat(filepath.Join(helperDir, executableName("lazymind-update-helper")))
	if err != nil {
		return sum, err
	}
	if !helper.Mode().IsRegular() {
		return sum, fs.ErrInvalid
	}

	digest := sha256.New()
	buf := make([]byte, 64*1024)
	err = filepath.WalkDir(bundle, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var st unix.Stat_t
		if err := unix.Lstat(path, &st); err != nil {
			return err
		}
		generation, err := startupObjectGeneration(path)
		if err != nil {
			return err
		}
		// lstat includes the symlink itself. Access times change during normal
		// verification; inode change times also capture writes that restore mtime.
		io.WriteString(digest, path+"\x00")
		if err := binary.Write(digest, binary.LittleEndian, [...]uint64{
			uint64(st.Dev), uint64(st.Ino), uint64(st.Mode), uint64(st.Size),
			uint64(st.Ctim.Sec), uint64(st.Ctim.Nsec), uint64(generation),
		}); err != nil {
			return err
		}
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			return nil
		case unix.S_IFLNK:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			io.WriteString(digest, target+"\x00")
			return nil
		case unix.S_IFREG:
			// macOS supplies a per-object data generation. Since device and
			// inode are also bound, the kernel can detect a changed file
			// without rereading the entire app before and after codesign.
			if generation != 0 {
				return nil
			}
			// Filesystems differ in timestamp resolution. Verify unchanged
			// bytes even for an in-place replacement of the same length.
			file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			defer file.Close()
			return hashStartupIdentityBytes(ctx, digest, file, buf)
		default:
			return fs.ErrInvalid
		}
	})
	if err != nil {
		return sum, err
	}
	digest.Sum(sum[:0])
	return sum, nil
}
