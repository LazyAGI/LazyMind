package mcpclient

import (
	"context"
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

func dshProfileName() string {
	if name := strings.TrimSpace(os.Getenv("LAZYMIND_DSH_PROFILE")); name != "" {
		return name
	}
	return "web"
}

// Resolve both ordinary profile-local and existing hoisted pnpm installations.
func dshModuleDir(profile, module string) (string, error) {
	for _, parent := range []string{profile, filepath.Dir(profile), filepath.Join(profile, "node_modules", ".pnpm")} {
		dir := filepath.Join(parent, "node_modules", filepath.FromSlash(module))
		if _, err := os.Stat(filepath.Join(dir, "package.json")); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return "", err
		}
		return dir, nil
	}
	return "", os.ErrNotExist
}

func dshModuleVersion(profile, module string) (string, error) {
	dir, err := dshModuleDir(profile, module)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(filepath.Join(dir, "package.json"))
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

func configureDSHWorkflow(path, configPath, command, bundleHash string, disabled bool) error {
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
		value["config"] = map[string]string{"connectorConfig": configPath, "command": command, "bundleHash": bundleHash}
	}
	var replacement yaml.Node
	if err := replacement.Encode(value); err != nil {
		return err
	}
	if existing == nil {
		sequence.Content = append(sequence.Content, &replacement)
	} else if disabled {
		*existing = replacement
	} else {
		disabledValue := mappingValue(&replacement, "disabled")
		setDSHYAML(existing, "disabled", disabledValue)
		config := mappingValue(existing, "config")
		replacementConfig := mappingValue(&replacement, "config")
		if config == nil || config.Kind != yaml.MappingNode {
			setDSHYAML(existing, "config", replacementConfig)
		} else {
			for _, key := range []string{"connectorConfig", "command", "bundleHash"} {
				setDSHYAML(config, key, mappingValue(replacementConfig, key))
			}
			// Earlier generated rows named the pairing file directly.
			for index := 0; index < len(config.Content); index += 2 {
				if config.Content[index].Value == "pairingFile" {
					config.Content = append(config.Content[:index], config.Content[index+2:]...)
					break
				}
			}
		}
	}
	body, err := encodeYAML(document)
	if err != nil {
		return err
	}
	return writeConfigFile(path, body)
}

func setDSHYAML(mapping *yaml.Node, key string, value *yaml.Node) {
	if existing := mappingValue(mapping, key); existing != nil {
		*existing = *value
	} else {
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	}
}

func (a *Adapter) setDSHPluginHash(hash string) error {
	path := a.dshConfigPath()
	doc, err := readYAMLDocument(path)
	if err != nil {
		return err
	}
	for _, row := range yamlSequence(doc).Content {
		if scalarValue(row, "id") != dshWorkflowRow {
			continue
		}
		config := mappingValue(row, "config")
		if config == nil && scalarValue(row, "disabled") == "true" {
			return nil
		}
		if config == nil || config.Kind != yaml.MappingNode {
			return errors.New("DSH workflow patch is missing its connection configuration")
		}
		if scalarValue(config, "bundleHash") == hash {
			return nil
		}
		setDSHYAML(config, "bundleHash", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: hash})
		body, err := encodeYAML(doc)
		if err != nil {
			return err
		}
		return writeConfigFile(path, body)
	}
	return errors.New("DSH workflow patch is missing")
}

func (a *Adapter) dshWorkflowConfigured() bool {
	record, err := a.dshPluginRecord()
	return err == nil && record.Selected.Release != "" && a.dshWorkflowConfiguration(true)
}

// Installation survives a native plugin toggle, sign-out or a revoked pairing.
// Those are reported separately as runtime prerequisites, not a missing bundle.
func (a *Adapter) dshWorkflowInstalled() bool {
	return a.dshWorkflowConfiguration(false)
}

func (a *Adapter) dshWorkflowConfiguration(requireEnabled bool) bool {
	return a.dshWorkflowConfigurationVersion(requireEnabled, true)
}

// Only identify an update when the installed package and every other managed
// configuration field are valid. Missing or unreadable files keep their errors.
func (a *Adapter) dshWorkflowNeedsUpdate() bool {
	return !a.dshWorkflowInstalled() && a.dshWorkflowConfigurationVersion(false, false)
}

func (a *Adapter) dshWorkflowConfigurationVersion(requireEnabled, currentBundle bool) bool {
	document, err := readYAMLDocument(a.dshConfigPath())
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
		if requireEnabled && !active || configErr != nil || !c.Controlled {
			return false
		}
	}
	for _, item := range yamlSequence(document).Content {
		if scalarValue(item, "id") != dshWorkflowRow || requireEnabled && scalarValue(item, "disabled") == "true" {
			continue
		}
		config := mappingValue(item, "config")
		if currentBundle {
			record, err := a.dshPluginRecord()
			if err != nil {
				return false
			}
			hash := scalarValue(config, "bundleHash")
			if record.Selected.Release == "" {
				digest, err := hex.DecodeString(hash)
				if err != nil || len(digest) != 32 {
					return false
				}
			} else {
				if record.Selected != record.Installed || record.Step != "restart" || hash != record.Installed.Release {
					return false
				}
				if _, err := a.checkDSHPlugin(record.Installed.Release); err != nil {
					return false
				}
			}
		}
		c, err := connectorconfig.Load(scalarValue(config, "connectorConfig"))
		if err != nil || c.Home != a.home || c.Provider != string(DeepSeekHarness) || c.Profile != filepath.Dir(a.dshConfigPath()) || c.HostID != a.hostID || !c.Controlled {
			return false
		}
		file := c.PairingFile
		id := strings.TrimSuffix(filepath.Base(file), ".json")
		if filepath.Clean(file) != filepath.Join(a.home, "workflow-hosts", id+".json") {
			return false
		}
		if !requireEnabled {
			return true
		}
		store, err := credentials.NewStore(a.home, "")
		if err != nil {
			return false
		}
		account, err := store.AccountScope()
		return err == nil && workflowhost.Configured(a.home, id, account)
	}
	return false
}

func (a *Adapter) runNativeDSHInstall(ctx context.Context, archive string) error {
	var version string
	var err error
	profile := filepath.Dir(a.dshConfigPath())
	if a.dshCLI == "" {
		version, err = dshModuleVersion(profile, "@deepseek-ai/dsh-session")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if errors.Is(err, os.ErrNotExist) {
			// Legacy standalone installations still obtain their matching package version.
			binary, findErr := dshExecutable()
			if findErr != nil {
				return findErr
			}
			output, probeErr := agentexec.Run(ctx, binary, "--version")
			if probeErr != nil {
				return probeErr
			}
			version = strings.TrimPrefix(strings.TrimSpace(output), "v")
		}

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
	return run(ctx, a.dshProfile(), args...)
}

func (a *Adapter) installDSHWorkflow(ctx context.Context, beforePublish func() error) (dshPluginRecord, error) {
	profile := filepath.Dir(a.dshConfigPath())
	store, err := credentials.NewStore(a.home, "")
	if err != nil {
		return dshPluginRecord{}, err
	}
	api, err := coreapi.New(store)
	if err != nil {
		return dshPluginRecord{}, err
	}
	var capability struct {
		SchemaReady bool   `json:"schema_ready"`
		Protocol    string `json:"protocol"`
	}
	if err := api.DoJSON(ctx, "GET", "/workflow-control/capabilities", nil, &capability); err != nil {
		return dshPluginRecord{}, err
	}
	if !capability.SchemaReady || capability.Protocol != "workflow.control.v1" {
		return dshPluginRecord{}, errors.New("update LazyMind Core and its database before enabling workflow control")
	}
	account, err := store.AccountScope()
	if err != nil {
		return dshPluginRecord{}, err
	}
	record, err := a.installDSHPlugin(ctx, "connect", "")
	if err != nil {
		return record, err
	}
	if err := beforePublish(); err != nil {
		return record, err
	}
	pair, err := workflowhost.Ensure(a.home, profile, account)
	if err != nil {
		return dshPluginRecord{}, err
	}
	pairingFile := filepath.Join(a.home, "workflow-hosts", pair.ConnectorID+".json")
	configPath, err := connectorconfig.Save(connectorconfig.Config{Home: a.home, Provider: string(DeepSeekHarness), HostID: a.hostID, Profile: profile, PairingFile: pairingFile, Controlled: true})
	if err != nil {
		return dshPluginRecord{}, err
	}
	if err := configureDSHWorkflow(a.dshConfigPath(), configPath, a.self, record.Selected.Release, false); err != nil {
		return dshPluginRecord{}, err
	}
	return record, nil
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
			file = ""
			if err == nil && c.Home == a.home && c.Provider == string(DeepSeekHarness) {
				file = c.PairingFile
			}
		}
		id := strings.TrimSuffix(filepath.Base(file), ".json")
		if file != "" && filepath.Dir(filepath.Clean(file)) == filepath.Join(a.home, "workflow-hosts") {
			if err := workflowhost.Disable(a.home, id); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return configureDSHWorkflow(a.dshConfigPath(), "", "", "", true)
	}
	return nil
}
