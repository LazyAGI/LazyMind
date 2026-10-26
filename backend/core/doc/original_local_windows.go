package doc

import (
	"fmt"
	"os"
	"strings"
)

func openLocalOriginal(path string) (*os.File, error) {
	if strings.HasPrefix(path, `\\.\`) || strings.HasPrefix(path, `\\?\`) {
		return nil, fmt.Errorf("device paths are not supported")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("regular file required")
	}
	return os.Open(path)
}
