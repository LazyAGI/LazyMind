// A portable process-boundary fake, not evidence that real RAG imports work.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if filename := os.Getenv("LAZYMIND_FIXTURE_COMPONENT_LOCK_PATH"); filename != "" {
		if canLock(filename) {
			fmt.Print("locked")
		} else {
			fmt.Print("busy")
		}
		return
	}
	executable, _ := os.Executable()
	if filename := os.Getenv("LAZYMIND_FIXTURE_COMPONENT_PROBE_LOG"); filename != "" {
		raw, _ := json.Marshal(map[string]any{"executable": executable, "args": os.Args[1:]})
		_ = os.WriteFile(filename, raw, 0600)
	}
	if strings.Contains(executable, "source-bundle") || os.Getenv("LAZYMIND_FIXTURE_COMPONENT_PROBE_FAIL") == "1" {
		os.Exit(17)
	}
	if root := os.Getenv("LAZYMIND_FIXTURE_PROBE_HOLD_DIR"); root != "" {
		if !withLock(filepath.Join(root, "alive.lock"), func() {
			_ = os.WriteFile(filepath.Join(root, "entered"), []byte("entered"), 0600)
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(root, "release")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			fmt.Print("fixture-late-success")
		}) {
			os.Exit(18)
		}
	}
}
