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
		if os.Getenv("LAZYMIND_FIXTURE_COMPONENT_PROBE_APPEND") == "1" {
			file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
			if err != nil {
				os.Exit(19)
			}
			_, err = file.Write(append(raw, '\n'))
			if file.Close() != nil || err != nil {
				os.Exit(19)
			}
		} else {
			_ = os.WriteFile(filename, raw, 0600)
		}
	}
	if strings.Contains(executable, "source-bundle") || os.Getenv("LAZYMIND_FIXTURE_COMPONENT_PROBE_FAIL") == "1" {
		os.Exit(17)
	}
	if path := os.Getenv("LAZYMIND_FIXTURE_COMPONENT_PROBE_FAIL_PATH"); path != "" {
		for _, arg := range os.Args[1:] {
			if strings.Contains(arg, path) {
				os.Exit(17)
			}
		}
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
