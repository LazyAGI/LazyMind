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
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

const dshSDKVersion = "0.1.2-rc.1"
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

func dshExecutable() (string, error) {
	return agentexec.FindBoundExecutable("", "LAZYMIND_DSH_PATH", agentexec.DeepSeekHarnessCLI, []string{"dsh"})
}

func runDSHPlugin(ctx context.Context, profile string, args ...string) error {
	dsh, err := dshExecutable()
	if err != nil {
		return errors.New("DeepSeek Harness was not found; select your existing DSH executable in LazyMind settings")
	}
	binary := dsh
	arguments := append([]string{"plugin", "--profile", profile}, args...)
	if _, err := agentexec.FindExecutable("", []string{"pnpm"}); err != nil {
		// Supply only DSH's package manager dependency. Never download another DSH.
		npx, err := agentexec.FindExecutable("", []string{"npx"})
		if err != nil {
			return errors.New("pnpm or Node.js/npm is required to install the LazyMind plugin into DSH")
		}
		binary = npx
		arguments = append([]string{"--yes", "--package=pnpm@10.0.0", "--", dsh}, arguments...)
	}
	var output strings.Builder
	err = (agentexec.StreamCommand{Binary: binary, Arguments: arguments,
		Environment: agentexec.SafeEnvironment("DSH_HOME=" + dshHome()),
	}).Run(ctx, func(line []byte) error {
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

func configureDSHWorkflow(path, pairingFile, webURL, bridgeURL string, disabled bool) error {
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
		value["config"] = map[string]string{"serverName": "lazymind", "pairingFile": pairingFile, "webUrl": webURL, "bridgeUrl": bridgeURL, "bundleHash": dshBundleHash()}
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
	document, err := readYAMLDocument(configPath(DeepSeekHarness))
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
	if _, err := dshModuleVersion(filepath.Dir(configPath(DeepSeekHarness)), dshWorkflowPackage); err != nil {
		return false
	}
	for _, item := range yamlSequence(document).Content {
		if scalarValue(item, "id") != dshWorkflowRow || scalarValue(item, "disabled") == "true" {
			continue
		}
		config := mappingValue(item, "config")
		if scalarValue(config, "bundleHash") != dshBundleHash() {
			return false
		}
		file := scalarValue(config, "pairingFile")
		id := strings.TrimSuffix(filepath.Base(file), ".json")
		if filepath.Clean(file) != filepath.Join(a.home, "workflow-hosts", id+".json") {
			return false
		}
		return workflowhost.Configured(a.home, id, account)
	}
	return false
}

func (a *Adapter) installDSHWorkflow(ctx context.Context) (bool, error) {
	profile := filepath.Dir(configPath(DeepSeekHarness))
	version, err := dshModuleVersion(profile, "@deepseek-ai/dsh-session")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if errors.Is(err, os.ErrNotExist) {
		// A new official profile inherits its base from the launching CLI and has no
		// local SDK dependencies. Install our MCP dependency through the supported CLI.
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
	if version != dshSDKVersion {
		if _, err := dshModuleVersion(profile, dshWorkflowPackage); err == nil {
			if err := configureDSHWorkflow(configPath(DeepSeekHarness), "", "", "", true); err != nil {
				return false, err
			}
		}
		return false, nil
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
	pair, err := workflowhost.Ensure(a.home, profile, account)
	if err != nil {
		return false, err
	}
	webURL := strings.TrimSpace(os.Getenv("LAZYMIND_WEB_URL"))
	if webURL == "" {
		webURL, err = store.ServerURL(ctx)
		if err != nil {
			return false, err
		}
	}
	bridgeURL := strings.TrimSpace(os.Getenv("LAZYMIND_ASSISTANT_BRIDGE_URL"))
	if bridgeURL == "" {
		bridgeURL = "http://127.0.0.1:19091"
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
	}
	if err := run(ctx, dshProfileName(), "add", "--workspace-root", "@deepseek-ai/dsh-mcp-client@"+dshSDKVersion, archive); err != nil {
		return false, err
	}
	pairingFile := filepath.Join(a.home, "workflow-hosts", pair.ConnectorID+".json")
	if err := configureDSHWorkflow(configPath(DeepSeekHarness), pairingFile, webURL, bridgeURL, false); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Adapter) disconnectDSHWorkflow() error {
	document, err := readYAMLDocument(configPath(DeepSeekHarness))
	if err != nil {
		return err
	}
	for _, item := range yamlSequence(document).Content {
		if scalarValue(item, "id") != dshWorkflowRow {
			continue
		}
		file := scalarValue(mappingValue(item, "config"), "pairingFile")
		id := strings.TrimSuffix(filepath.Base(file), ".json")
		if file != "" {
			if err := workflowhost.Disable(a.home, id); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return configureDSHWorkflow(configPath(DeepSeekHarness), "", "", "", true)
	}
	return nil
}
