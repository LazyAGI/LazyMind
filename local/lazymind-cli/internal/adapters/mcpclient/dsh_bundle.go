package mcpclient

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

const dshWorkflowRow = "lazymind-workflow-panel"
const dshWorkflowPackage = "@lazymind/dsh-workflow"

//go:embed assets/dsh-workflow.tgz
var dshWorkflowArchive []byte

func dshBundleHash() string {
	sum := sha256.Sum256(dshWorkflowArchive)
	return hex.EncodeToString(sum[:])
}

func dshProfileName() string {
	if name := strings.TrimSpace(os.Getenv("LAZYMIND_DSH_PROFILE")); name != "" {
		return name
	}
	return "web"
}

// Resolve both ordinary profile-local and existing hoisted pnpm installations.
func dshModuleVersion(profile, module string) (string, error) {
	for _, parent := range []string{profile, filepath.Dir(profile), filepath.Join(profile, "node_modules", ".pnpm")} {
		body, err := os.ReadFile(filepath.Join(parent, "node_modules", filepath.FromSlash(module), "package.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(body, &manifest); err != nil {
			return "", err
		}
		return manifest.Version, nil
	}
	return "", os.ErrNotExist
}

func dshBinaryNames() []string {
	return []string{"dsh", "dsh.cmd", "dsh.exe"}
}

func dshLocalExecutableCandidates() []string {
	home := dshHome()
	profile := dshProfileDir()
	var candidates []string
	for _, name := range dshBinaryNames() {
		candidates = append(candidates,
			filepath.Join(profile, "node_modules", ".bin", name),
			filepath.Join(home, "node_modules", ".bin", name),
			filepath.Join(home, "bin", name),
		)
	}
	return candidates
}

func dshExecutable() (string, error) {
	if override := connectorconfig.Value("LAZYMIND_DSH_PATH"); override != "" {
		resolved, err := agentexec.ResolveExecutable(override)
		if err != nil {
			return "", fmt.Errorf("LAZYMIND_DSH_PATH: %w", err)
		}
		return resolved, nil
	}
	if binding, found, err := agentexec.ExecutableBinding(agentexec.DeepSeekHarnessCLI); err != nil {
		return "", err
	} else if found {
		resolved, err := agentexec.ResolveExecutable(binding)
		if err != nil {
			return "", fmt.Errorf("saved DeepSeek Harness program binding: %w", err)
		}
		return resolved, nil
	}
	if resolved, err := agentexec.FindExecutable("", dshBinaryNames()); err == nil {
		return resolved, nil
	}
	for _, candidate := range dshLocalExecutableCandidates() {
		if resolved, err := agentexec.ResolveExecutable(candidate); err == nil {
			return resolved, nil
		}
	}
	return "", errors.New("DeepSeek Harness CLI was not found")
}

// Keep discovery and process execution injectable without changing global state.
type dshPluginRuntime struct {
	findDSH        func() (string, error)
	findExecutable func(string) (string, error)
	run            func(context.Context, string, []string, func([]byte) error) error
}

func runDSHPlugin(ctx context.Context, profile string, args ...string) error {
	return runDSHPluginWith(ctx, dshPluginRuntime{
		findDSH: dshExecutable,
		findExecutable: func(name string) (string, error) {
			return agentexec.FindExecutable("", []string{name})
		},
		run: func(ctx context.Context, binary string, arguments []string, output func([]byte) error) error {
			return (agentexec.StreamCommand{Binary: binary, Arguments: arguments,
				Environment: agentexec.SafeEnvironment("DSH_HOME=" + dshHome()),
			}).Run(ctx, output)
		},
	}, profile, args...)
}

func runDSHPluginWith(ctx context.Context, runtime dshPluginRuntime, profile string, args ...string) error {
	dsh, err := runtime.findDSH()
	if err != nil {
		return errors.New("DeepSeek Harness CLI was not found; start DSH Web once so LazyMind can use the existing installation")
	}
	binary := dsh
	arguments := append([]string{"plugin", "--profile", profile}, args...)
	if _, err := runtime.findExecutable("pnpm"); err != nil {
		// Supply only DSH's package manager dependency. Never download another DSH.
		npx, err := runtime.findExecutable("npx")
		if err != nil {
			return errors.New("pnpm or Node.js/npm is required to install the LazyMind plugin into DSH")
		}
		binary = npx
		arguments = append([]string{"--yes", "--package=pnpm@10.0.0", "--", dsh}, arguments...)
	}
	var output strings.Builder
	err = runtime.run(ctx, binary, arguments, func(line []byte) error {
		if output.Len() < 2048 {
			output.Write(line)
			output.WriteByte('\n')
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("install LazyMind plugin into DSH: %w (%s)", err, strings.TrimSpace(output.String()))
	}
	return nil
}

func configureDSHWorkflow(path, configPath, command string, disabled bool) error {
	document, err := readYAMLDocument(path)
	if err != nil {
		return err
	}
	sequence := yamlSequence(document)
	var existing *yaml.Node
	for _, item := range sequence.Content {
		if scalarValue(item, "id") == dshWorkflowRow {
			existing = item
			break
		}
	}
	value := map[string]any{"id": dshWorkflowRow, "disabled": disabled}
	if !disabled {
		value["config"] = map[string]string{"connectorConfig": configPath, "command": command, "bundleHash": dshBundleHash()}
	}
	var replacement yaml.Node
	if err := replacement.Encode(value); err != nil {
		return err
	}
	if existing != nil {
		*existing = replacement
	} else {
		sequence.Content = append(sequence.Content, &replacement)
	}
	body, err := encodeYAML(document)
	if err != nil {
		return err
	}
	return writeConfigFile(path, body)
}

func (a *Adapter) dshWorkflowConfigured() bool {
	document, err := readYAMLDocument(a.dshConfigPath())
	if err != nil {
		return false
	}
	store, err := credentials.NewStore(a.home, "")
	if err != nil {
		return false
	}
	account, err := store.AccountScope()
	if err != nil {
		return false
	}
	if _, err := dshModuleVersion(filepath.Dir(a.dshConfigPath()), dshWorkflowPackage); err != nil {
		return false
	}
	if a.dshTarget != "" {
		if !a.dshMCPReady {
			return false
		}
		body, err := os.ReadFile(filepath.Join(filepath.Dir(a.dshConfigPath()), "package.json"))
		var manifest struct {
			DSH struct {
				Profile struct {
					Bundles []string `json:"bundles"`
				} `json:"profile"`
			} `json:"dsh"`
		}
		if err != nil || json.Unmarshal(body, &manifest) != nil {
			return false
		}
		active := false
		for _, bundle := range manifest.DSH.Profile.Bundles {
			if bundle == dshWorkflowPackage {
				active = true
			}
		}
		c, configErr := connectorconfig.Load(decodeDSHStdio(findDSHEntry(document)).Env[connectorconfig.EnvironmentKey])
		if !active || configErr != nil || !c.Controlled {
			return false
		}
	}
	for _, item := range yamlSequence(document).Content {
		if scalarValue(item, "id") != dshWorkflowRow || scalarValue(item, "disabled") == "true" {
			continue
		}
		config := mappingValue(item, "config")
		if scalarValue(config, "bundleHash") != dshBundleHash() {
			return false
		}
		c, err := connectorconfig.Load(scalarValue(config, "connectorConfig"))
		if err != nil || c.Home != a.home || c.Provider != string(DeepSeekHarness) || c.Profile != filepath.Dir(a.dshConfigPath()) {
			return false
		}
		file := c.PairingFile
		id := strings.TrimSuffix(filepath.Base(file), ".json")
		if filepath.Clean(file) != filepath.Join(a.home, "workflow-hosts", id+".json") {
			return false
		}
		return workflowhost.Configured(a.home, id, account)
	}
	return false
}

func (a *Adapter) installDSHWorkflow(ctx context.Context, beforePublish ...func() error) (bool, error) {
	var version string
	var err error
	profile := filepath.Dir(a.dshConfigPath())
	if a.dshCLI == "" {
		version, err = dshModuleVersion(profile, "@deepseek-ai/dsh-session")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		if errors.Is(err, os.ErrNotExist) {
			// Legacy standalone installations still obtain their matching package version.
			binary, findErr := dshExecutable()
			if findErr != nil {
				return false, findErr
			}
			output, probeErr := agentexec.Run(ctx, binary, "--version")
			if probeErr != nil {
				return false, probeErr
			}
			version = strings.TrimPrefix(strings.TrimSpace(output), "v")
		}

	}
	store, err := credentials.NewStore(a.home, "")
	if err != nil {
		return false, err
	}
	api, err := coreapi.New(store)
	if err != nil {
		return false, err
	}
	var capability struct {
		SchemaReady bool   `json:"schema_ready"`
		Protocol    string `json:"protocol"`
	}
	if err := api.DoJSON(ctx, "GET", "/workflow-control/capabilities", nil, &capability); err != nil {
		return false, err
	}
	if !capability.SchemaReady || capability.Protocol != "workflow.control.v1" {
		return false, errors.New("update LazyMind Core and its database before enabling workflow control")
	}
	account, err := store.AccountScope()
	if err != nil {
		return false, err
	}
	archive := filepath.Join(a.home, "bundles", "dsh-workflow-"+dshBundleHash()[:16]+".tgz")
	if err := os.MkdirAll(filepath.Dir(archive), 0700); err != nil {
		return false, err
	}
	if err := os.WriteFile(archive, dshWorkflowArchive, 0600); err != nil {
		return false, err
	}
	run := a.dshRunner
	if run == nil {
		run = runDSHPlugin
		if a.dshCLI != "" {
			run = a.runBundledDSHPlugin
		}
	}
	args := []string{"add", "--workspace-root", "@deepseek-ai/dsh-mcp-client@" + version, archive}
	if a.dshCLI != "" {
		args = []string{"add", archive}
	}
	if err := run(ctx, a.dshProfile(), args...); err != nil {
		return false, err
	}
	for _, prepare := range beforePublish {
		if err := prepare(); err != nil {
			return false, err
		}
	}
	pair, err := workflowhost.Ensure(a.home, profile, account)
	if err != nil {
		return false, err
	}
	pairingFile := filepath.Join(a.home, "workflow-hosts", pair.ConnectorID+".json")
	configPath, err := connectorconfig.Save(connectorconfig.Config{Home: a.home, Provider: string(DeepSeekHarness), HostID: a.hostID, Profile: profile, PairingFile: pairingFile, Controlled: true})
	if err != nil {
		return false, err
	}
	if err := configureDSHWorkflow(a.dshConfigPath(), configPath, a.self, false); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Adapter) disconnectDSHWorkflow() error {
	document, err := readYAMLDocument(a.dshConfigPath())
	if err != nil {
		return err
	}
	for _, item := range yamlSequence(document).Content {
		if scalarValue(item, "id") != dshWorkflowRow {
			continue
		}
		config := mappingValue(item, "config")
		file := scalarValue(config, "pairingFile") // Migration of the previous generated format.
		if entry := scalarValue(config, "connectorConfig"); entry != "" {
			c, err := connectorconfig.Load(entry)
			if err != nil {
				return err
			}
			if c.Home != a.home {
				return errors.New("DSH connection belongs to another installation")
			}
			file = c.PairingFile
		}
		id := strings.TrimSuffix(filepath.Base(file), ".json")
		if file != "" {
			if err := workflowhost.Disable(a.home, id); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return configureDSHWorkflow(a.dshConfigPath(), "", "", true)
	}
	return nil
}
