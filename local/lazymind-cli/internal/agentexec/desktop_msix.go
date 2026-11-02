package agentexec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Keep package selection and manifest path resolution portable so they can be
// tested without a Windows installation. Only the system query is platform-specific.
type windowsAppPackage struct {
	Name            string
	InstallLocation string
	Executables     []string
}

func findWindowsDesktopApplication(spec DesktopApplication, traditional func(DesktopApplication) string, query func([]string) ([]byte, error)) string {
	if executable := traditional(spec); executable != "" {
		return executable
	}
	paths := findWindowsPackagedDesktopApplications(spec, query)
	if len(paths) > 0 {
		return paths[0]
	}
	return ""
}

func findWindowsPackagedDesktopApplications(spec DesktopApplication, query func([]string) ([]byte, error)) []string {
	if len(spec.PackageNames) == 0 {
		return nil
	}
	body, err := query(spec.PackageNames)
	if err != nil {
		return nil
	}
	var packages []windowsAppPackage
	if json.Unmarshal([]byte(strings.TrimPrefix(string(body), "\ufeff")), &packages) != nil {
		return nil
	}
	var paths []string
	for _, pkg := range packages {
		if !containsFold(spec.PackageNames, pkg.Name) || !filepath.IsAbs(pkg.InstallLocation) {
			continue
		}
		for _, executable := range pkg.Executables {
			// Manifest executables are relative to the package root. Reject drive,
			// UNC and escaping paths even when parsing Windows fixtures on macOS.
			relative := filepath.FromSlash(strings.ReplaceAll(executable, `\`, "/"))
			if strings.ContainsAny(relative, ":\x00") || !filepath.IsLocal(relative) || !containsFold(spec.ExecutableNames, filepath.Base(relative)) {
				continue
			}
			candidate := filepath.Join(pkg.InstallLocation, relative)
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				paths = append(paths, candidate)
			}
		}
	}
	return paths
}

func containsFold(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}

func windowsAppPackageQuery(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, "'"+strings.ReplaceAll(name, "'", "''")+"'")
	}
	return `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$packages = @(Get-AppxPackage -PackageTypeFilter Main -ErrorAction Stop | Where-Object { $_.Name -in @(` + strings.Join(quoted, ",") + `) } | Sort-Object Version -Descending)
$result = @(foreach ($package in $packages) {
    try {
        $manifest = Get-AppxPackageManifest -Package $package.PackageFullName -ErrorAction Stop
        [PSCustomObject]@{
            Name = $package.Name
            InstallLocation = $package.InstallLocation
            Executables = @($manifest.Package.Applications.Application | ForEach-Object { [string]$_.Executable })
        }
    } catch { continue }
})
ConvertTo-Json -InputObject $result -Depth 4 -Compress
`
}
