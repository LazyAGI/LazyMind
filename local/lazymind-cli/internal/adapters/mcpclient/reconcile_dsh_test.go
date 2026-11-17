package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"gopkg.in/yaml.v3"

	"lazymind/agentconnector/internal/connectorconfig"
)

func TestReconcileDSHPreservesDisabledRowsAndUpdatesEveryOwnedProfile(t *testing.T) {
	root := t.TempDir()
	setTestHome(t, root)
	t.Setenv("DSH_HOME", filepath.Join(root, "DSH"))
	a := testAdapter(DeepSeekHarness)
	a.home = filepath.Join(root, "state")
	a.self, _ = os.Executable()
	for _, name := range []string{"desktop", "web", "custom"} {
		profile := filepath.Join(dshHome(), "profiles", name)
		reference, err := connectorconfig.Save(connectorconfig.Config{Home: a.home, HostID: a.hostID, Provider: string(DeepSeekHarness), Profile: profile, Controlled: false})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(profile, "cordis.patch.yml")
		old := filepath.Join(root, "removed desktop", "lazymind")
		patch := dshProfilePatch(old, connectorconfig.Environment(reference)) +
			"- id: " + dshWorkflowRow + "\n  disabled: true\n  custom: keep\n  config:\n    connectorConfig: " + strconv.Quote(reference) + "\n    command: " + strconv.Quote(old) + "\n    bundleHash: old-bundle\n" +
			"- id: unrelated\n  disabled: true\n  config:\n    keep: value\n"
		writeTestFile(t, path, patch)
		doc, err := readYAMLDocument(path)
		if err != nil {
			t.Fatal(err)
		}
		// Both native MCP disable switches are independent of Workflow's switch.
		entry := findDSHEntry(doc)
		appendYAMLBool(entry, "disabled", true)
		appendYAMLBool(mappingValue(entry, "insert").Content[0], "disabled", true)
		before, err := encodeYAML(doc)
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, path, string(before))
	}
	global := filepath.Join(dshHome(), "cordis.patch.yml")
	const globalPatch = "# unrelated global settings\n- id: unrelated\n  disabled: true\n"
	writeTestFile(t, global, globalPatch)
	if err := a.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"desktop", "web", "custom"} {
		path := filepath.Join(dshHome(), "profiles", name, "cordis.patch.yml")
		doc, err := readYAMLDocument(path)
		if err != nil {
			t.Fatal(err)
		}
		entry := findDSHEntry(doc)
		if decodeDSHStdio(entry).Command != a.self || scalarValue(entry, "disabled") != "true" ||
			scalarValue(mappingValue(entry, "insert").Content[0], "disabled") != "true" {
			t.Fatal("MCP command or disabled choices lost")
		}
		for _, row := range yamlSequence(doc).Content {
			if scalarValue(row, "id") == dshWorkflowRow {
				if scalarValue(row, "disabled") != "true" || scalarValue(row, "custom") != "keep" ||
					scalarValue(mappingValue(row, "config"), "command") != a.self || scalarValue(mappingValue(row, "config"), "bundleHash") != "old-bundle" {
					t.Fatal("Workflow command or disabled choices lost")
				}
			}
		}
		backup, err := os.ReadFile(path + ".lazymind-backup")
		if err != nil || bytes.Contains(backup, []byte(a.self)) {
			t.Fatal("missing original patch backup", err)
		}
		assertReconcileDoesNotRewrite(t, a, path)
	}
	body, _ := os.ReadFile(global)
	if string(body) != globalPatch || pathExists(a.dshRecordPath()) {
		t.Fatal("migration changed installation intent or unrelated global settings")
	}
}

func appendYAMLBool(mapping *yaml.Node, key string, value bool) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(value)})
}

func TestReconcileDSHRejectsOtherProfileOrHostAndUnsupportedRows(t *testing.T) {
	for _, scenario := range []string{"profile", "host", "provider", "home", "duplicate", "multi-row"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			setTestHome(t, root)
			t.Setenv("DSH_HOME", filepath.Join(root, "DSH"))
			a := testAdapter(DeepSeekHarness)
			a.home = filepath.Join(root, "state")
			a.self, _ = os.Executable()
			profile := filepath.Join(dshHome(), "profiles", "web")
			c := connectorconfig.Config{Home: a.home, HostID: a.hostID, Provider: string(DeepSeekHarness), Profile: profile}
			switch scenario {
			case "profile":
				c.Profile = filepath.Join(dshHome(), "profiles", "another")
			case "host":
				c.HostID = "foreign"
			case "provider":
				c.Provider = string(Cursor)
			case "home":
				c.Home = filepath.Join(root, "other-state")
			}
			reference, err := connectorconfig.Save(c)
			if err != nil {
				t.Fatal(err)
			}
			patch := dshProfilePatch(filepath.Join(root, "removed", "lazymind"), connectorconfig.Environment(reference))
			if scenario == "duplicate" {
				patch += patch
			} else if scenario == "multi-row" {
				patch += "    - id: other\n      name: other\n"
			}
			path := filepath.Join(profile, "cordis.patch.yml")
			writeTestFile(t, path, patch)
			err = a.Reconcile(context.Background())
			if (err != nil) != (scenario == "duplicate" || scenario == "multi-row") {
				t.Fatalf("unexpected error: %v", err)
			}
			body, _ := os.ReadFile(path)
			if string(body) != patch {
				t.Fatal("foreign or ambiguous DSH settings changed")
			}
		})
	}
}

func TestReconcileDSHLegacyUsesScopedPairingWithoutRewritingEnvironment(t *testing.T) {
	for _, scenario := range []string{"env pairing", "workflow pairing", "legacy pairing", "foreign profile", "missing profile evidence"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			setTestHome(t, root)
			t.Setenv("DSH_HOME", filepath.Join(root, "DSH"))
			a := testAdapter(DeepSeekHarness)
			a.home = filepath.Join(root, "state")
			a.self, _ = os.Executable()
			profile := filepath.Join(dshHome(), "profiles", "web")
			pair := map[string]string{"provider": string(DeepSeekHarness), "profile": profile, "connector_id": "dsh-0123456789abcdef0123456789abcdef"}
			if scenario == "foreign profile" {
				pair["profile"] = filepath.Join(dshHome(), "profiles", "foreign")
			} else if scenario == "legacy pairing" {
				delete(pair, "provider")
			}
			pairingFile := filepath.Join(a.home, "workflow-hosts", pair["connector_id"]+".json")
			body, _ := json.Marshal(pair)
			writeTestFile(t, pairingFile, string(body))
			old := legacyTestStdio(filepath.Join(root, "removed", "lazymind"), a.home, a.hostID, DeepSeekHarness)
			if scenario == "env pairing" {
				old.Env["LAZYMIND_WORKFLOW_PAIRING_FILE"] = pairingFile
			}
			patch := dshProfilePatch(old.Command, old.Env)
			if scenario != "env pairing" && scenario != "missing profile evidence" {
				patch += "- id: " + dshWorkflowRow + "\n  disabled: true\n  config:\n    pairingFile: " + strconv.Quote(pairingFile) + "\n"
			}
			path := filepath.Join(profile, "cordis.patch.yml")
			writeTestFile(t, path, patch)
			if err := a.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			doc, _ := readYAMLDocument(path)
			after := decodeDSHStdio(findDSHEntry(doc))
			wantCommand := a.self
			if scenario == "foreign profile" || scenario == "missing profile evidence" {
				wantCommand = old.Command
			}
			if after.Command != wantCommand {
				t.Fatalf("command=%q want=%q", after.Command, wantCommand)
			}
			beforeEnv, _ := json.Marshal(old.Env)
			afterEnv, _ := json.Marshal(after.Env)
			if !bytes.Equal(beforeEnv, afterEnv) {
				t.Fatal("legacy ownership evidence changed the actual MCP environment")
			}
		})
	}
}
