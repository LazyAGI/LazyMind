package desktopupdate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const darwinSessionPrefix = "darwin-session-v1:"

var bootUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func DarwinBootSession(raw string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(raw))
	if !bootUUID.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
		return "", failure("handoff_state_unknown", nil)
	}
	return darwinSessionPrefix + id, nil
}

func validDarwinSession(session string) bool {
	raw, ok := strings.CutPrefix(session, darwinSessionPrefix)
	canonical, err := DarwinBootSession(raw)
	return ok && err == nil && session == canonical
}

type bootObservation struct {
	TransactionID string `json:"transactionId"`
	BootSession   string `json:"bootSession"`
}

func bootObservationPath(root, tx string) string {
	return filepath.Join(root, "native-v2", tx+".boot-session.json")
}

func writeBootObservation(root, tx, session string) error {
	raw, err := json.Marshal(bootObservation{tx, session})
	if err == nil {
		err = atomicWrite(bootObservationPath(root, tx), raw)
	}
	return err
}

func removeBootObservation(root, tx string) error {
	err := os.Remove(bootObservationPath(root, tx))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Called under the state and component locks, after signed repair acceptance.
// A legacy Darwin display hash cannot establish a reboot. First clear it so
// older readers fail closed, then record the current session and require a
// later boot. A crash between writes therefore only delays recovery.
func verifyRepairBoot(root string, r *NativeRecord, current string) error {
	if r.Platform == "windows" {
		if !digestPattern.MatchString(current) || !digestPattern.MatchString(r.BootID) || current == r.BootID {
			return failure("handoff_restart_required", nil)
		}
		return nil
	}
	if r.Platform != "darwin" || !validDarwinSession(current) || (r.BootID != "" && !digestPattern.MatchString(r.BootID)) {
		return failure("handoff_state_unknown", nil)
	}
	var prior bootObservation
	err := readMetadata(bootObservationPath(root, r.TransactionID), &prior)
	if err == nil {
		if prior.TransactionID != r.TransactionID || !validDarwinSession(prior.BootSession) {
			return failure("handoff_state_unknown", nil)
		}
		if r.BootID == "" {
			if prior.BootSession == current {
				return failure("handoff_restart_required", nil)
			}
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return failure("handoff_state_unknown", err)
	}
	if r.BootID != "" {
		r.BootID = ""
		if err := writeNativeRecord(root, r); err != nil {
			return failure("update_record_write", err)
		}
	}
	if err := writeBootObservation(root, r.TransactionID, current); err != nil {
		return failure("update_record_write", err)
	}
	return failure("handoff_restart_required", nil)
}

func removeNativeRecord(root, tx string) error {
	if err := removeBootObservation(root, tx); err != nil {
		return err
	}
	file := NativeRecordPath(root)
	if err := os.Remove(file); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(file))
}
