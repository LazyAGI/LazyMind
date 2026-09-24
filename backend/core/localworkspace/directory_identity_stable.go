//go:build darwin

package localworkspace

import "os"

// The path observation and the opened object must refer to the same object.
// Never substitute the identity of a replacement encountered during open.
func checkedOpenedIdentity(path string, before os.FileInfo, file *os.File) (string, error) {
	after, err := file.Stat()
	if err != nil {
		return "", err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if current.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, after) || !os.SameFile(current, after) {
		return "", os.ErrInvalid
	}
	return openedDirectoryIdentity(file)
}
