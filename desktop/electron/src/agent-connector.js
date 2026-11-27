const { execFile } = require("node:child_process");

const agentConnectorActionTimeoutMs = 15 * 1000;
const agentConnectorStatusTimeoutMs = 30 * 1000;
const agentConnectorDisconnectTimeoutMs = 60 * 1000;
const agentConnectorInstallTimeoutMs = 660 * 1000;
const agentConnectorWorkBuddyInstallTimeoutMs = 120 * 1000;
const agentConnectorCodexInstallTimeoutMs = 120 * 1000;

function createAgentConnector({ runConnectorJSON, startAgentLogin, withAgentLoginStatus, agentRecovery }) {
  return function runAgentConnector(agent, action, refreshInstallations = false) {
    const allowedActions = {
      all: new Set(["status"]),
      codex: new Set(["connect", "status", "disconnect", "login"]),
      cursor: new Set(["connect", "status", "disconnect", "login"]),
      workbuddy: new Set(["connect", "status", "disconnect", "login"]),
      raccoon: new Set(["connect", "status", "disconnect"]),
      traework: new Set(["connect", "status", "disconnect"]),
      "deepseek-harness": new Set(["connect", "status", "disconnect"]),
    };
    if (!allowedActions[agent]?.has(action)) {
      return Promise.reject(new Error(`Unsupported external Agent action: ${agent}/${action}`));
    }
    if (action === "login") {
      return startAgentLogin(agent);
    }
    const installWorkflow = ["deepseek-harness", "workbuddy", "codex"].includes(agent) && action === "connect";
    const run = () => runConnectorJSON(
      ["internal", "agent", agent, action, ...(action === "status" && refreshInstallations === true ? ["--refresh-installations"] : [])],
      installWorkflow ? (agent === "workbuddy" ? agentConnectorWorkBuddyInstallTimeoutMs
        : agent === "codex" ? agentConnectorCodexInstallTimeoutMs : agentConnectorInstallTimeoutMs)
        : agent === "deepseek-harness" && action === "disconnect" ? agentConnectorDisconnectTimeoutMs
        : action === "status" ? agentConnectorStatusTimeoutMs : agentConnectorActionTimeoutMs,
    );
    if (installWorkflow) {
      return runConnectorJSON(["assistant", "start"], agentConnectorActionTimeoutMs).then(run).then(result => {
        if (agent === "codex") agentRecovery.start();
        return result;
      });
    }
    if (agent === "codex" && action === "disconnect") {
      return agentRecovery.stop().then(run).then(result => withAgentLoginStatus(agent, result));
    }
    return run().then((result) => withAgentLoginStatus(agent, result));
  };
}

function createConnectorJSONRunner({ executable, environment, windowsHide, execute = execFile }) {
  return function runConnectorJSON(args, timeout, input, options = {}) {
    return new Promise((resolve, reject) => {
      const child = execute(executable, args, {
        env: environment(),
        timeout,
        windowsHide,
        signal: options.signal,
      }, (error, stdout, stderr) => {
        if (error) {
          error.message = String(stderr || stdout || error.message).trim();
          reject(error);
          return;
        }
        try {
          resolve(JSON.parse(stdout));
        } catch (parseError) {
          reject(new Error(`LazyMind connector returned invalid JSON: ${parseError.message}`));
        }
      });
      if (input !== undefined) {
        child.stdin?.end(`${JSON.stringify(input)}\n`);
      }
    });
  };
}

function registerAgentConnectorActions(ipcMain, runAgentConnector) {
  ipcMain.handle("lazymind:agentIntegrationStatuses", (_event, refreshInstallations) => runAgentConnector("all", "status", refreshInstallations === true));
  ipcMain.handle("lazymind:agentIntegrationAction", (_event, agent, action) => runAgentConnector(agent, action));
}

module.exports = { agentConnectorActionTimeoutMs, createAgentConnector, createConnectorJSONRunner, registerAgentConnectorActions };
