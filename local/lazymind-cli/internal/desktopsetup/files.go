// Package desktopsetup manages reversible desktop startup resources.
package desktopsetup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"lazymind/agentconnector/internal/localfile"
)

type File struct {
	Before        []byte      `json:"before,omitempty"`
	Existed       bool        `json:"existed"`
	BeforeMode    os.FileMode `json:"before_mode"`
	After         []byte      `json:"after"`
	Mode          os.FileMode `json:"mode"`
	PendingBefore []byte      `json:"pending_before,omitempty"`
	Pending       bool        `json:"pending,omitempty"`
}
type Files struct {
	Path    string          `json:"-"`
	Entries map[string]File `json:"files"`
}

// Caller holds the provider setup lock throughout installation/restore.
func Load(path string) (*Files, error) {
	f := &Files{Path: path, Entries: map[string]File{}}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(body, f) != nil || f.Entries == nil {
		return nil, fmt.Errorf("invalid startup ownership: %s", path)
	}
	return f, nil
}
func (f *Files) save() error {
	body, _ := json.Marshal(f)
	return localfile.Write(f.Path, body, 0600)
}

func (f *Files) Put(path string, body []byte, mode os.FileMode) error {
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	old, owned := f.Entries[path]
	if owned && err == nil && !bytes.Equal(current, old.After) && !(old.Pending && bytes.Equal(current, old.PendingBefore)) {
		return fmt.Errorf("startup file was changed outside LazyMind: %s", path)
	}
	if !owned {
		old = File{Before: current, Existed: err == nil}
		if err == nil {
			info, e := os.Stat(path)
			if e != nil {
				return e
			}
			old.BeforeMode = info.Mode().Perm()
		}
	}
	old.PendingBefore, old.Pending = current, true
	old.After, old.Mode = body, mode
	f.Entries[path] = old
	// Journal before writing, including new files, so a partial install is reversible.
	if err := f.save(); err != nil {
		return err
	}
	if err := localfile.Write(path, body, mode); err != nil {
		return err
	}
	old.PendingBefore, old.Pending = nil, false
	f.Entries[path] = old
	return f.save()
}

func (f *Files) Check() error {
	if len(f.Entries) == 0 {
		return fmt.Errorf("startup configuration is missing")
	}
	for path, expected := range f.Entries {
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(body, expected.After) {
			return fmt.Errorf("startup configuration changed: %s", path)
		}
		// Windows does not expose POSIX executable permission bits through Stat.
		if runtime.GOOS != "windows" && expected.Mode&0111 != 0 {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if info.Mode().Perm()&0111 == 0 {
				return fmt.Errorf("startup launcher is not executable: %s", path)
			}
		}
	}
	return nil
}

func (f *Files) Restore() error {
	for path, old := range f.Entries {
		body, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && (bytes.Equal(body, old.After) || old.Pending && bytes.Equal(body, old.PendingBefore)) {
			if old.Existed {
				err = localfile.Write(path, old.Before, old.BeforeMode)
			} else {
				err = os.Remove(path)
			}
			if err != nil {
				return err
			}
		}
		// Preserve external edits and deletions instead of restoring over them.
		delete(f.Entries, path)
		if err := f.save(); err != nil {
			return err
		}
	}
	return nil
}

func Lock(home, provider string) (func(), error) {
	dir := filepath.Join(home, provider+"-workflow")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return localfile.Lock(filepath.Join(dir, "startup.lock"))
}

func ShellQuote(s string) string {
	var b bytes.Buffer
	b.WriteByte('\'')
	for _, c := range s {
		if c == '\'' {
			b.WriteString("'\\''")
		} else {
			b.WriteRune(c)
		}
	}
	b.WriteByte('\'')
	return b.String()
}
