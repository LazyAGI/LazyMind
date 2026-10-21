package mcpclient

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
)

//go:embed dsh_runtime_probe.cjs
var dshRuntimeProbe string

//go:embed dsh_runtime_paths.cjs
var dshRuntimePaths string

type dshRuntimeProfile struct {
	Name       string `json:"name"`
	MCPVersion string `json:"mcpVersion"`
	MCPPath    string `json:"mcpPath"`
	Path       string `json:"path"`
	Diagnostic string `json:"diagnostic"`
}
type dshRuntimeInspection struct {
	Version    string              `json:"version"`
	Supported  bool                `json:"supported"`
	Profiles   []dshRuntimeProfile `json:"profiles"`
	Path       string              `json:"path"`
	Diagnostic string              `json:"diagnostic"`
}

// Use the bundled Electron, not the user's Node or pnpm. The probe reads metadata
// with DSH's own resolver and never boots an app or evaluates a profile patch.
func inspectDSHRuntime(ctx context.Context, cli string) (dshRuntimeInspection, error) {
	var result dshRuntimeInspection
	resources := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(cli))))
	binary := filepath.Join(filepath.Dir(resources), "MacOS", "DeepSeek Harness")
	if runtime.GOOS == "windows" {
		binary = filepath.Join(filepath.Dir(resources), "DeepSeek Harness.exe")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	spec := agentexec.StreamCommand{Binary: binary, Arguments: []string{"--expose-internals", "-e", dshRuntimePaths + "\n" + dshRuntimeProbe, resources, dshHome()}, Environment: agentexec.SafeEnvironment("ELECTRON_RUN_AS_NODE=1", "DSH_HOME="+dshHome())}
	var output strings.Builder
	err := spec.Run(ctx, func(line []byte) error { output.Write(line); return nil })
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(output.String()), &result); err != nil {
		return result, fmt.Errorf("read DSH inspection: %w", err)
	}
	return result, nil
}

func (a *Adapter) inspectDSH(ctx context.Context, cli string) (dshRuntimeInspection, error) {
	if a.dshInspector != nil {
		return a.dshInspector(ctx, cli)
	}
	return inspectDSHRuntime(ctx, cli)
}

func dshIssue(code, path string) *agentintegration.Issue {
	return &agentintegration.Issue{Code: code, Path: path}
}

// Persisted diagnostics from earlier builds are classified without exposing their
// raw output in the UI. Unknown errors never become a version or dependency claim.
func dshRecordedIssue(message, profile string) *agentintegration.Issue {
	if strings.Contains(message, "ERR_PNPM_UNEXPECTED_STORE") {
		return dshIssue("dependency_environment", filepath.Join(dshHome(), "profiles", profile))
	}
	return dshIssue("operation_failed", "")
}

func sanitizeDSHStatus(s *agentintegration.Status) {
	if s.State != agentintegration.Failed && s.State != agentintegration.Conflict {
		return
	}
	if s.Issue == nil {
		s.Issue = dshIssue("operation_failed", "")
	}
	switch s.Issue.Code {
	case "unsupported_version":
		s.Message = "This DSH version is not supported. Update to 0.2.0-rc.2 or later and retry."
	case "dependency_environment":
		s.Message = "The DSH dependency environment is invalid. Unable to connect."
	default:
		s.Message = "DSH connection failed. Retry, and report the issue if it persists."
	}
	for i := range s.Profiles {
		if s.Profiles[i].Message != "" {
			s.Profiles[i].Message = s.Message
		}
	}
}

func dshFailure(s agentintegration.Status, err error) agentintegration.Status {
	log.Printf("DSH integration: %v", err)
	s = agentintegration.Fail(s, err.Error())
	s.Issue = dshIssue("operation_failed", "")
	sanitizeDSHStatus(&s)
	return s
}

func dshInspectionIssue(result dshRuntimeInspection, err error, cli string) *agentintegration.Issue {
	if err != nil || result.Diagnostic != "" {
		log.Printf("DSH inspection: %v %s", err, result.Diagnostic)
		path := result.Path
		if path == "" {
			path = filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(cli))))
		}
		return dshIssue("dependency_environment", path)
	}
	if !result.Supported {
		return dshIssue("unsupported_version", "")
	}
	if len(result.Profiles) != len(dshDesktopProfiles) {
		return dshIssue("dependency_environment", dshHome())
	}
	for _, profile := range result.Profiles {
		if profile.Diagnostic != "" || profile.MCPPath == "" || profile.MCPVersion == "" {
			log.Printf("DSH %s inspection: %s", profile.Name, profile.Diagnostic)
			path := profile.Path
			if path == "" {
				path = filepath.Join(dshHome(), "profiles", profile.Name)
			}
			return dshIssue("dependency_environment", path)
		}
	}
	return nil
}
