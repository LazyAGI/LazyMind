import { execFile, type ExecException, type ExecFileOptionsWithStringEncoding } from "node:child_process";
import { writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { createInstance } from "i18next";
import { I18nextProvider } from "react-i18next";
import { afterEach, expect, it, vi } from "vitest";
import enUS from "@/i18n/locales/en-US";
import AgentIntegrationPage from "./AgentIntegrationPage";

vi.mock("@/modules/chat/utils/request", () => ({
  ConversationSettingsApi: () => ({
    listChatExecutors: async () => ({ data: { data: { executors: [] } } }),
  }),
}));

const requireDesktop = createRequire(import.meta.url);
const { installDesktopBridge } = requireDesktop("../../../../desktop/electron/src/preload.js");
const { createAgentConnector, createConnectorJSONRunner, registerAgentConnectorActions } =
  requireDesktop("../../../../desktop/electron/src/agent-connector.js");

afterEach(() => vi.unstubAllGlobals());

it("installs DSH after confirming the first connection in Desktop settings", async () => {
  const executable = process.env.LAZYMIND_TEST_DESKTOP_DSH_CONNECTOR_EXE;
  const address = process.env.LAZYMIND_TEST_DESKTOP_DSH_ADDRESS;
  const resultPath = process.env.LAZYMIND_TEST_DESKTOP_DSH_RESULT;
  if (!executable || !address || !resultPath) {
    throw new Error("Run through TestDesktopDSHFirstConnectReplacesLegacyBridge to supply the native fixture");
  }

  let connectStderr = "";
  const runConnectorJSON = createConnectorJSONRunner({
    executable,
    environment: () => process.env,
    windowsHide: process.platform === "win32",
    execute(
      file: string, args: string[], options: ExecFileOptionsWithStringEncoding,
      callback: (error: ExecException | null, stdout: string, stderr: string) => void,
    ) {
      // Keep the production executable and route its Bridge to the private test listener.
      const command = args[0] === "assistant" && args[1] === "start" ? [...args, "--listen", address] : args;
      return execFile(file, command, options, (error, stdout, stderr) => {
        if (args[0] === "internal" && args[2] === "deepseek-harness" && args[3] === "connect") connectStderr = stderr;
        callback(error, stdout, stderr);
      });
    },
  });
  const runAgentConnector = createAgentConnector({
    runConnectorJSON,
    withAgentLoginStatus: (_agent: string, status: unknown) => status,
  });

  // Connect the real preload to the registered handlers through an in-process IPC transport.
  type Handler = (event: object, ...args: unknown[]) => unknown;
  const handlers = new Map<string, Handler>([
    ["lazymind:executorIntegrationPolicies", () => ({ executors: {} })],
    ["lazymind:agentExecutableBindings", () => ({ bindings: {} })],
  ]);
  registerAgentConnectorActions({
    handle: (channel: string, listener: Handler) => handlers.set(channel, listener),
  }, runAgentConnector);
  const actions: { args: unknown[]; result: Promise<unknown> }[] = [];
  installDesktopBridge({ exposeInMainWorld: vi.stubGlobal }, {
    invoke: (channel: string, ...args: unknown[]) => {
      const handler = handlers.get(channel);
      if (!handler) throw new Error(`Unexpected Desktop IPC channel: ${channel}`);
      const result = Promise.resolve().then(() => handler({}, ...args));
      if (channel === "lazymind:agentIntegrationAction") actions.push({ args, result });
      return result;
    },
  });

  const i18n = createInstance();
  await i18n.init({ lng: "en-US", resources: { "en-US": { translation: enUS } } });
  render(
    <ConfigProvider theme={{ token: { motion: false } }}>
      <I18nextProvider i18n={i18n}><AgentIntegrationPage /></I18nextProvider>
    </ConfigProvider>,
  );
  const panel = within(screen.getByTestId("agent-panel-deepseek-harness"));
  fireEvent.click(panel.getByRole("button", { name: /DeepSeek Harness/ }));
  const toggle = await waitFor(() => {
    const element = panel.getByRole("switch", { name: "DeepSeek Harness uses LazyMind MCP" });
    expect(element).toBeEnabled();
    return element;
  }, { timeout: 35_000 });
  expect(toggle).not.toBeChecked();
  fireEvent.click(toggle);

  const guide = enUS.agentIntegration.dshInstallGuide;
  const dialog = within(await screen.findByRole("dialog", { name: guide.title }));
  expect(dialog.getByText(guide.prepareDescription)).toBeInTheDocument();
  expect(actions).toHaveLength(0);
  fireEvent.click(dialog.getByRole("button", { name: guide.continue }));

  await waitFor(() => expect(actions.map(({ args }) => args)).toEqual([["deepseek-harness", "connect"]]));
  let status: unknown;
  await act(async () => { status = await actions[0].result; });
  expect(status, connectStderr || JSON.stringify(status)).toMatchObject({
    state: "enabled",
    connection: { mcp_configured: true, runtime_state: "waiting" },
  });
  await dialog.findByText(guide.installedTitle);
  expect(toggle).toBeChecked();
  await writeFile(resultPath, JSON.stringify(status));
});
