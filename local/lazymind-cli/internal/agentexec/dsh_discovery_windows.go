//go:build windows

package agentexec

import (
	"os"
	"path/filepath"
)

// Reuse Windows installation registrations, then inspect only immediate install
// directories. Every candidate still passes the Desktop bundle validation.
func platformDSHDesktopCandidates() []string {
	var roots []string
	if executable := appPath("DeepSeek Harness.exe"); executable != "" {
		roots = append(roots, filepath.Dir(executable))
	}
	for _, app := range windowsInstalledApplications() {
		if matchesInstalledDisplayName(app.displayName, []string{"deepseek harness"}) {
			roots = append(roots, app.location)
		}
	}
	parents := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		parents = append(parents, filepath.Join(local, "Programs"))
	}
	for _, parent := range uniqueWindowsPaths(parents) {
		entries, _ := os.ReadDir(parent)
		for i, entry := range entries {
			if i >= windowsExecutableScanLimit {
				break
			}
			if entry.IsDir() {
				roots = append(roots, filepath.Join(parent, entry.Name()))
			}
		}
	}
	var candidates []string
	for _, root := range uniqueWindowsPaths(roots) {
		candidates = append(candidates, filepath.Join(root, "resources", "runtime", "cli", "bin", "dsh.cmd"))
	}
	return candidates
}
