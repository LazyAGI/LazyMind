package conversationgroup

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"sort"

	"github.com/google/uuid"
)

var temporaryGroupID = regexp.MustCompile(`^new_[1-9][0-9]*$`)

// Persist before the first request so retries and restored tasks see the same IDs.
func ensureGroupIDs(cp *incrementalCheckpoint, cards map[string]directoryCard) bool {
	if cp.GroupIDs == nil {
		cp.GroupIDs = map[string]string{}
	}
	ids := make([]string, 0, len(cards))
	for id := range cards {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	changed := false
	for _, id := range ids {
		if cp.GroupIDs[id] == "" {
			cp.NextGroupNumber++
			cp.GroupIDs[id] = fmt.Sprintf("g%d", cp.NextGroupNumber)
			changed = true
		}
	}
	return changed
}

func mappedDirectory(cards map[string]directoryCard, cp incrementalCheckpoint) []directoryCard {
	out := sortedCards(cards)
	for i := range out {
		out[i].ShortID = cp.GroupIDs[out[i].ID]
		out[i].Examples = nil
	}
	return out
}

// Only staged copies are changed. The caller commits the mapping with pending
// operations, then commits candidate rows and assignments after scope audits.
func decodeGroupIDs(out *organizerStepOutput, cp incrementalCheckpoint, cards map[string]directoryCard) (map[string]string, int, error) {
	ids := maps.Clone(cp.GroupIDs)
	if ids == nil {
		ids = map[string]string{}
	}
	next := cp.NextGroupNumber
	reverse := map[string]string{"free": "free"}
	for id, short := range ids {
		if _, exists := cards[id]; !exists {
			continue
		}
		target, err := directoryTarget(cards, id)
		if err != nil {
			return nil, next, err
		}
		reverse[short] = target
	}
	resolve := func(short string) (string, error) {
		if id, ok := reverse[short]; ok {
			return id, nil
		}
		return "", errors.New("unknown or cyclic candidate target")
	}
	for i := range out.Operations {
		op := &out.Operations[i]
		if op.Op == "create" {
			if !temporaryGroupID.MatchString(op.ID) || reverse[op.ID] != "" {
				return nil, next, errors.New("invalid candidate operation")
			}
			id := "cand_" + uuid.NewString()
			reverse[op.ID] = id
			next++
			ids[id] = fmt.Sprintf("g%d", next)
			op.ID = id
		} else if op.Op == "merge" {
			var err error
			op.TargetID, err = resolve(op.TargetID)
			if err != nil {
				return nil, next, err
			}
			for j, source := range op.SourceIDs {
				op.SourceIDs[j], err = resolve(source)
				if err != nil {
					return nil, next, err
				}
			}
		} else {
			var err error
			op.ID, err = resolve(op.ID)
			if err != nil {
				return nil, next, err
			}
		}
	}
	for i := range out.Assignments {
		var err error
		out.Assignments[i].GroupID, err = resolve(out.Assignments[i].GroupID)
		if err != nil {
			return nil, next, err
		}
	}
	return ids, next, nil
}
