// Package download fetches official knowledge base packages from third-party
// sources. The package URL decides the strategy: ".git" URLs are cloned and
// any git-lfs pointers are replaced via per-host resolve rules; any other URL
// is downloaded directly and unzipped when the payload is a zip archive.
package download

// FetchedFile describes one file materialized inside the destination dir.
type FetchedFile struct {
	Path   string // relative path inside dstDir
	Size   int64
	SHA256 string
}

// ProgressFunc reports download progress; total < 0 means unknown.
type ProgressFunc func(done, total int64)
