package chatagent

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const imageModTimeTolerance = 2 * time.Second

// ImageAttachmentsSince returns regular image files created or updated in one
// provider-owned output directory during the current run.
func ImageAttachmentsSince(directory string, since time.Time) ([]Attachment, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	attachments := make([]Attachment, 0)
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		mediaType := imageMediaType(entry.Name())
		if mediaType == "" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		// Filesystems do not all preserve sub-second modification times. Compare
		// against the coarsest common two-second timestamp resolution so an image
		// written immediately after the run starts is not dropped on another OS.
		if !info.Mode().IsRegular() || info.ModTime().Before(since.Add(-imageModTimeTolerance)) {
			continue
		}
		attachments = append(attachments, Attachment{
			Path: filepath.Join(directory, entry.Name()), Filename: entry.Name(), MediaType: mediaType,
		})
	}
	sort.Slice(attachments, func(i, j int) bool { return attachments[i].Filename < attachments[j].Filename })
	return attachments, nil
}

func imageMediaType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}
