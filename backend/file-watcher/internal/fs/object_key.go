package fs

import (
	"fmt"
	"path/filepath"
	"strings"
)

func pathObjectKey(agentID, path string) string {
	return fmt.Sprintf("local_fs:%s:path:%s", strings.TrimSpace(agentID), filepath.Clean(strings.TrimSpace(path)))
}
