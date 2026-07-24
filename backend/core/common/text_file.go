package common

import "strings"

var textFileExtensions = map[string]struct{}{
	"txt": {}, "md": {}, "markdown": {}, "csv": {}, "tsv": {},
	"json": {}, "jsonl": {}, "ndjson": {}, "xml": {},
	"yaml": {}, "yml": {}, "toml": {}, "ini": {}, "cfg": {}, "conf": {},
	"log": {}, "sql": {}, "html": {}, "htm": {},
	"css": {}, "scss": {}, "sass": {}, "less": {},
	"py": {}, "pyi": {}, "js": {}, "jsx": {}, "mjs": {}, "cjs": {},
	"ts": {}, "tsx": {}, "java": {}, "c": {}, "h": {}, "cc": {},
	"cpp": {}, "cxx": {}, "hpp": {}, "cs": {}, "go": {}, "rs": {},
	"rb": {}, "php": {}, "swift": {}, "kt": {}, "kts": {}, "scala": {},
	"sh": {}, "bash": {}, "zsh": {}, "fish": {}, "ps1": {}, "bat": {}, "cmd": {},
	"vue": {}, "svelte": {}, "tex": {}, "rst": {}, "properties": {}, "env": {},
	"gradle": {}, "groovy": {}, "lua": {}, "r": {}, "dart": {},
	"ex": {}, "exs": {}, "erl": {}, "hrl": {}, "clj": {}, "cljs": {},
	"edn": {}, "fs": {}, "fsx": {}, "vb": {}, "asm": {}, "s": {},
}

func IsTextFileExtension(extension string) bool {
	extension = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(extension)), ".")
	_, ok := textFileExtensions[extension]
	return ok
}
