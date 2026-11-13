package academic

import (
	"net/url"
	"path/filepath"
	"strings"
)

// sqliteFileURL encodes an absolute filesystem path without treating a Windows
// drive letter as a URI authority. URL also escapes ?, # and % in file names so
// they cannot become SQLite connection parameters.
func sqliteFileURL(filename string) *url.URL {
	path := filepath.ToSlash(filename)
	if len(path) >= 3 && path[1] == ':' && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && (path[2] == '/' || path[2] == '\\') {
		path = "/" + strings.ReplaceAll(path, "\\", "/")
	}
	return &url.URL{Scheme: "file", Path: path}
}
