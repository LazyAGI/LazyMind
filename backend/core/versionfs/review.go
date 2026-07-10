package versionfs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"strconv"
	"strings"
)

func AnnotateReviewFile(file *ReviewFile, session ReviewSessionMeta, decisions map[string]string, canUndo bool) {
	EnsureSyntheticHunk(file)
	file.HunkCount = 0
	file.PendingCount = 0
	file.AcceptedCount = 0
	file.RejectedCount = 0

	hunkIndex := 0
	for i := range file.DiffLines {
		if file.DiffLines[i].Type != "HUNK" {
			continue
		}
		hunkIndex++
		end := len(file.DiffLines)
		for j := i + 1; j < len(file.DiffLines); j++ {
			if file.DiffLines[j].Type == "HUNK" {
				end = j
				break
			}
		}
		id, oldStart, oldLines, newStart, newLines := HunkID(session.ID, file.Path, file.Status, hunkIndex, file.DiffLines[i:end])
		decision := decisions[DecisionKey(file.Path, id)]
		if decision == "" {
			decision = decisions[id]
		}
		if decision == "" {
			decision = DecisionPending
		}
		file.DiffLines[i].HunkID = id
		file.DiffLines[i].Decision = decision
		file.DiffLines[i].OldStart = oldStart
		file.DiffLines[i].OldLines = oldLines
		file.DiffLines[i].NewStart = newStart
		file.DiffLines[i].NewLines = newLines
	}

	file.ReviewID = session.ID
	file.ReviewVersion = session.Version
	file.DraftVersion = session.DraftVersion
	file.BaseRevisionID = session.BaseRevisionID
	file.DraftSnapshotHash = session.DraftSnapshotHash
	file.CanUndo = canUndo
	for _, line := range file.DiffLines {
		if line.Type != "HUNK" {
			continue
		}
		file.HunkCount++
		switch line.Decision {
		case DecisionAccepted:
			file.AcceptedCount++
		case DecisionRejected:
			file.RejectedCount++
		default:
			file.PendingCount++
		}
	}
}

func EnsureSyntheticHunk(file *ReviewFile) {
	if len(file.DiffLines) > 0 || file.Type != EntryTypeFile || file.Status == "unchanged" {
		return
	}
	text := "@@ file " + file.Status + " @@"
	file.DiffLines = []DiffLine{{
		Type:     "HUNK",
		Text:     text,
		HTML:     html.EscapeString(text),
		OldLine:  1,
		NewLine:  1,
		OldStart: 1,
		NewStart: 1,
	}}
}

func HunkID(sessionID, path, status string, index int, lines []DiffLine) (string, int, int, int, int) {
	oldStart, newStart := 0, 0
	oldLines, newLines := 0, 0
	var deleted, added []string
	for _, line := range lines {
		switch line.Type {
		case "CONTEXT":
			if line.OldLine > 0 {
				if oldStart == 0 {
					oldStart = line.OldLine
				}
				oldLines++
			}
			if line.NewLine > 0 {
				if newStart == 0 {
					newStart = line.NewLine
				}
				newLines++
			}
		case "DELETION":
			if line.OldLine > 0 {
				if oldStart == 0 {
					oldStart = line.OldLine
				}
				oldLines++
			}
			deleted = append(deleted, line.Text)
		case "ADDITION":
			if line.NewLine > 0 {
				if newStart == 0 {
					newStart = line.NewLine
				}
				newLines++
			}
			added = append(added, line.Text)
		case "HUNK":
			if line.OldLine > 0 && oldStart == 0 {
				oldStart = line.OldLine
			}
			if line.NewLine > 0 && newStart == 0 {
				newStart = line.NewLine
			}
		}
	}
	if oldStart == 0 {
		oldStart = 1
	}
	if newStart == 0 {
		newStart = 1
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		sessionID,
		path,
		status,
		strconv.Itoa(index),
		strconv.Itoa(oldStart),
		strconv.Itoa(newStart),
		hashStrings(deleted),
		hashStrings(added),
	}, "\x00")))
	return "hunk_" + fmt.Sprintf("%04d", index) + "_" + hex.EncodeToString(sum[:])[:12], oldStart, oldLines, newStart, newLines
}

func HunkLines(file ReviewFile) []DiffLine {
	out := []DiffLine{}
	for _, line := range file.DiffLines {
		if line.Type == "HUNK" {
			out = append(out, line)
		}
	}
	return out
}

func KnownHunks(file ReviewFile) map[string]DiffLine {
	out := map[string]DiffLine{}
	for _, line := range file.DiffLines {
		if line.Type == "HUNK" && strings.TrimSpace(line.HunkID) != "" {
			out[line.HunkID] = line
		}
	}
	return out
}

func MergeTextFile(file ReviewFile, decisions map[string]string) (string, error) {
	if file.Binary || file.TooLarge {
		return "", fmt.Errorf("cannot merge hunks")
	}
	var out []string
	currentDecision := DecisionPending
	for _, line := range file.DiffLines {
		switch line.Type {
		case "HUNK":
			currentDecision = decisions[DecisionKey(file.Path, line.HunkID)]
			if currentDecision == "" {
				currentDecision = decisions[line.HunkID]
			}
			if currentDecision == "" || currentDecision == DecisionPending {
				return "", fmt.Errorf("pending hunks exist")
			}
		case "CONTEXT":
			out = append(out, line.Text)
		case "DELETION":
			if currentDecision == DecisionRejected {
				out = append(out, line.Text)
			}
		case "ADDITION":
			if currentDecision == DecisionAccepted {
				out = append(out, line.Text)
			}
		}
	}
	content := strings.Join(out, "\n")
	if len(out) > 0 {
		content += "\n"
	}
	return content, nil
}

func ApplyTextReview(headContent, draftContent string, file ReviewFile, decisions map[string]string) (string, error) {
	hunks := HunkLines(file)
	if len(hunks) == 0 {
		return draftContent, nil
	}
	allAccepted := true
	allRejected := true
	normalized := map[string]string{}
	for _, hunk := range hunks {
		decision := decisions[DecisionKey(file.Path, hunk.HunkID)]
		if decision == "" {
			decision = decisions[hunk.HunkID]
		}
		if decision == "" {
			decision = DecisionAccepted
		}
		normalized[DecisionKey(file.Path, hunk.HunkID)] = decision
		if decision != DecisionAccepted {
			allAccepted = false
		}
		if decision != DecisionRejected {
			allRejected = false
		}
	}
	if allAccepted {
		return draftContent, nil
	}
	if allRejected {
		return headContent, nil
	}
	return MergeTextFile(file, normalized)
}

func DecisionKey(path, hunkID string) string {
	return path + "\x00" + hunkID
}

func hashStrings(values []string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\n")))
	return hex.EncodeToString(sum[:])
}
