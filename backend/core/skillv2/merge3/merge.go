package merge3

import (
	"bytes"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

type File struct {
	Data   []byte
	Binary bool
	Mode   int
}

type Conflict struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type Result struct {
	Files     map[string]File
	Conflicts []Conflict
}

func MergeTrees(base, ours, theirs map[string]File) Result {
	result := Result{Files: make(map[string]File)}
	for _, path := range unionPaths(base, ours, theirs) {
		baseFile, baseOK := base[path]
		oursFile, oursOK := ours[path]
		theirsFile, theirsOK := theirs[path]
		switch {
		case sameFile(oursFile, oursOK, theirsFile, theirsOK):
			putFile(result.Files, path, oursFile, oursOK)
		case sameFile(baseFile, baseOK, oursFile, oursOK):
			putFile(result.Files, path, theirsFile, theirsOK)
		case sameFile(baseFile, baseOK, theirsFile, theirsOK):
			putFile(result.Files, path, oursFile, oursOK)
		case !baseOK:
			putFile(result.Files, path, theirsFile, theirsOK)
			result.Conflicts = append(result.Conflicts, Conflict{Path: path, Kind: "both_added"})
		case !oursOK:
			putFile(result.Files, path, theirsFile, theirsOK)
			result.Conflicts = append(result.Conflicts, Conflict{Path: path, Kind: "user_deleted_platform_modified"})
		case !theirsOK:
			result.Conflicts = append(result.Conflicts, Conflict{Path: path, Kind: "user_modified_platform_deleted"})
		case baseFile.Binary || oursFile.Binary || theirsFile.Binary:
			putFile(result.Files, path, theirsFile, true)
			result.Conflicts = append(result.Conflicts, Conflict{Path: path, Kind: "binary"})
		default:
			merged, conflict := MergeText(baseFile.Data, oursFile.Data, theirsFile.Data)
			result.Files[path] = File{Data: merged, Mode: theirsFile.Mode}
			if conflict {
				result.Conflicts = append(result.Conflicts, Conflict{Path: path, Kind: "text"})
			}
		}
	}
	return result
}

func MergeText(base, ours, theirs []byte) ([]byte, bool) {
	baseLines := splitLines(string(base))
	oursEdits := diffEdits(baseLines, splitLines(string(ours)))
	theirsEdits := diffEdits(baseLines, splitLines(string(theirs)))
	merged, conflict := mergeEdits(baseLines, oursEdits, theirsEdits)
	return []byte(strings.Join(merged, "")), conflict
}

type edit struct {
	start int
	end   int
	lines []string
}

func diffEdits(base, target []string) []edit {
	opcodes := difflib.NewMatcher(base, target).GetOpCodes()
	edits := make([]edit, 0, len(opcodes))
	for _, opcode := range opcodes {
		if opcode.Tag == 'e' {
			continue
		}
		edits = append(edits, edit{start: opcode.I1, end: opcode.I2, lines: append([]string(nil), target[opcode.J1:opcode.J2]...)})
	}
	return edits
}

func mergeEdits(base []string, ours, theirs []edit) ([]string, bool) {
	merged := make([]string, 0, len(base))
	oursIndex, theirsIndex, position := 0, 0, 0
	hasConflict := false
	for oursIndex < len(ours) || theirsIndex < len(theirs) {
		start := nextEditStart(ours, oursIndex, theirs, theirsIndex)
		if position < start {
			merged = append(merged, base[position:start]...)
		}
		end := start
		oursEnd, theirsEnd := oursIndex, theirsIndex
		for {
			previousOursEnd, previousTheirsEnd, previousEnd := oursEnd, theirsEnd, end
			for oursEnd < len(ours) && editBelongsToCluster(ours[oursEnd], start, end) {
				if ours[oursEnd].end > end {
					end = ours[oursEnd].end
				}
				oursEnd++
			}
			for theirsEnd < len(theirs) && editBelongsToCluster(theirs[theirsEnd], start, end) {
				if theirs[theirsEnd].end > end {
					end = theirs[theirsEnd].end
				}
				theirsEnd++
			}
			if previousOursEnd == oursEnd && previousTheirsEnd == theirsEnd && previousEnd == end {
				break
			}
		}

		baseRegion := append([]string(nil), base[start:end]...)
		oursRegion := applyRegion(base, start, end, ours[oursIndex:oursEnd])
		theirsRegion := applyRegion(base, start, end, theirs[theirsIndex:theirsEnd])
		switch {
		case equalLines(oursRegion, theirsRegion):
			merged = append(merged, oursRegion...)
		case equalLines(oursRegion, baseRegion):
			merged = append(merged, theirsRegion...)
		case equalLines(theirsRegion, baseRegion):
			merged = append(merged, oursRegion...)
		default:
			merged = append(merged, theirsRegion...)
			hasConflict = true
		}
		position = end
		oursIndex, theirsIndex = oursEnd, theirsEnd
	}
	if position < len(base) {
		merged = append(merged, base[position:]...)
	}
	return merged, hasConflict
}

func editBelongsToCluster(value edit, start, end int) bool {
	if end == start {
		return value.start == start
	}
	return value.start < end
}

func applyRegion(base []string, start, end int, edits []edit) []string {
	if len(edits) == 0 {
		return append([]string(nil), base[start:end]...)
	}
	out := make([]string, 0, end-start)
	position := start
	for _, value := range edits {
		if position < value.start {
			out = append(out, base[position:value.start]...)
		}
		out = append(out, value.lines...)
		position = value.end
	}
	if position < end {
		out = append(out, base[position:end]...)
	}
	return out
}

func nextEditStart(ours []edit, oursIndex int, theirs []edit, theirsIndex int) int {
	if oursIndex >= len(ours) {
		return theirs[theirsIndex].start
	}
	if theirsIndex >= len(theirs) {
		return ours[oursIndex].start
	}
	if ours[oursIndex].start < theirs[theirsIndex].start {
		return ours[oursIndex].start
	}
	return theirs[theirsIndex].start
}

func splitLines(value string) []string {
	if value == "" {
		return nil
	}
	lines := make([]string, 0, strings.Count(value, "\n")+1)
	for len(value) > 0 {
		index := strings.IndexByte(value, '\n')
		if index < 0 {
			lines = append(lines, value)
			break
		}
		lines = append(lines, value[:index+1])
		value = value[index+1:]
	}
	return lines
}

func sameFile(left File, leftOK bool, right File, rightOK bool) bool {
	return leftOK == rightOK && (!leftOK || left.Binary == right.Binary && left.Mode == right.Mode && bytes.Equal(left.Data, right.Data))
}

func putFile(files map[string]File, path string, file File, exists bool) {
	if !exists {
		return
	}
	file.Data = append([]byte(nil), file.Data...)
	files[path] = file
}

func equalLines(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func unionPaths(values ...map[string]File) []string {
	seen := make(map[string]bool)
	for _, files := range values {
		for path := range files {
			seen[path] = true
		}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
