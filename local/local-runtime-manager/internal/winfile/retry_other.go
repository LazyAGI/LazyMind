//go:build !windows

package winfile

func retryableFilesystemError(error) bool {
	return false
}
