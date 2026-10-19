import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { parseArgs } from 'node:util';
import { execFileSync } from 'node:child_process';
import { loadConfig, atomicJSON } from './config.mjs';
import { desktopRunning } from './platform.mjs';
import { manageWindows } from './manage-windows.mjs';
import { disableActivation } from './activation.mjs';
import { restoreGeneratedPaths } from './restore-paths.mjs';
import { registerWorkflow, unregisterWorkflow, registrationPath, workflowRegistration } from './workflow-registration.mjs';

const { values, positionals } = parseArgs({ allowPositionals: true, options: { config: { type: 'string' }, 'current-cli': { type: 'string' } } });
const action = positionals[0];
if (!['plan', 'enable', 'disable', 'status', 'apply-login'].includes(action) || positionals.length !== 1) throw new Error('Use plan|enable|disable|status --config <runtime.json>');
if (!['darwin', 'win32'].includes(process.platform)) throw new Error('Unsupported platform');
const config = loadConfig(values.config);
if (process.platform === 'win32') { manageWindows(action, config, { currentCLI: values['current-cli'] }); process.exit(0); }
const directory = path.dirname(config.configFile);
const stateFile = path.join(directory, 'activation.json');
const agentFile = path.join(os.homedir(), 'Library/LaunchAgents/com.lazymind.codex-desktop.plist');
const label = 'com.lazymind.codex-desktop';
const domain = `gui/${process.getuid()}`;
const desired = {
  CODEX_CLI_PATH: path.join(directory, 'launch-adapter'), CODEX_APP_SERVER_FORCE_CLI: '1',
  CODEX_APP_SERVER_WS_URL: null, CODEX_APP_SERVER_USE_LOCAL_DAEMON: null,
};
const run = args => execFileSync('/bin/launchctl', args, { encoding: 'utf8', timeout: 10000, stdio: ['ignore', 'pipe', 'pipe'] });
const current = () => Object.fromEntries(Object.keys(desired).map(key => {
  try { return [key, run(['getenv', key]).replace(/\n$/, '') || null]; } catch (error) { if (error.status === 1) return [key, null]; throw error; }
}));
const set = values => { for (const [key, value] of Object.entries(values)) run(value === null ? ['unsetenv', key] : ['setenv', key, value]); };
const xml = value => value.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;');
const plist = `<?xml version="1.0" encoding="UTF-8"?>\n<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n<plist version="1.0"><dict><key>Label</key><string>${label}</string><key>ProgramArguments</key><array>${[
  config.node, path.join(config.bundleDir, 'manage.mjs'), 'apply-login', '--config', config.configFile,
].map(value => `<string>${xml(value)}</string>`).join('')}</array><key>RunAtLoad</key><true/><key>KeepAlive</key><false/></dict></plist>\n`;
const matches = (a, b) => Object.keys(desired).every(key => a[key] === b[key]);
const exists = fs.existsSync(stateFile);
const state = exists ? JSON.parse(fs.readFileSync(stateFile, 'utf8')) : null;
if (state && state.config !== config.configFile) throw new Error('Activation belongs to another installation');

if (action === 'plan') {
  process.stdout.write(JSON.stringify({ environment: desired, before: current(), launchAgent: agentFile, plist,
    workflowRegistration: { path: registrationPath(config), value: workflowRegistration(config) },
    note: 'Preview only; no GUI configuration changed and no App or daemon started.',
  }, null, 2) + '\n');
} else if (action === 'status') {
  process.stdout.write(JSON.stringify({ enabled: !!state, environmentMatches: matches(current(), desired), runtimeDir: config.runtimeDir,
    backend: fs.existsSync(path.join(config.runtimeDir, 'backend.json')) ? JSON.parse(fs.readFileSync(path.join(config.runtimeDir, 'backend.json'), 'utf8')).process : null,
    note: 'Backend is the last recorded identity, not a liveness check. No processes started.',
  }, null, 2) + '\n');
} else if (action === 'apply-login') {
  if (!state || state.disabling || !fs.existsSync(agentFile) || fs.readFileSync(agentFile, 'utf8') !== plist) throw new Error('No matching activation');
  set(desired); // Only configure future GUI launches; never start Desktop or daemon at login.
} else if (action === 'enable') {
  fs.accessSync(desired.CODEX_CLI_PATH, fs.constants.X_OK);
  if (state) {
    if (state.disabling) throw new Error('Finish disconnecting before enabling shared mode again');
    if (!fs.existsSync(agentFile) || fs.readFileSync(agentFile, 'utf8') !== plist || !matches(current(), desired)) throw new Error('Activation changed externally; inspect before enabling again');
    registerWorkflow(config);
  } else {
    if (fs.existsSync(agentFile)) throw new Error('LaunchAgent already belongs to another installation');
    try { run(['print', `${domain}/${label}`]); throw new Error('LaunchAgent label is already loaded'); }
    catch (error) { if (!error.status) throw error; }
    const before = current();
    atomicJSON(stateFile, { config: config.configFile, before });
    let registered = false;
    try {
      registerWorkflow(config); registered = true;
      fs.mkdirSync(path.dirname(agentFile), { recursive: true });
      fs.writeFileSync(agentFile, plist, { mode: 0o600, flag: 'wx' });
      set(desired);
      run(['bootstrap', domain, agentFile]);
    } catch (error) {
      try { run(['bootout', `${domain}/${label}`]); } catch { /* It may not have loaded. */ }
      set(before);
      if (registered) unregisterWorkflow(config);
      if (fs.existsSync(agentFile) && fs.readFileSync(agentFile, 'utf8') === plist) fs.unlinkSync(agentFile);
      fs.unlinkSync(stateFile);
      throw error;
    }
  }
  process.stdout.write('Shared mode configured. Quit Desktop normally, then open its original icon. No App or backend was started by this command.\n');
} else if (action === 'disable') {
  disableActivation(config, stateFile, state, {
    running: desktopRunning, set, unregister: unregisterWorkflow,
    validate: state => {
      if (fs.existsSync(agentFile) ? fs.readFileSync(agentFile, 'utf8') !== plist : !state.disabling) throw new Error('LaunchAgent changed externally; refusing removal');
      const now = current();
      const compatible = state.disabling
        ? Object.keys(desired).every(key => now[key] === desired[key] || now[key] === state.before[key])
        : matches(now, desired) || matches(now, state.before);
      if (!compatible) throw new Error('GUI environment changed externally; refusing to overwrite it');
    },
    stop: () => {
      // Unload first so login apply cannot race the restore.
      try { run(['bootout', `${domain}/${label}`]); }
      catch (error) { if (error.status !== 3 && error.status !== 113) throw error; }
    },
    restore: () => restoreGeneratedPaths(config, desired.CODEX_CLI_PATH),
    remove: () => { if (fs.existsSync(agentFile)) fs.unlinkSync(agentFile); },
  });
  process.stdout.write('Original GUI environment restored; owned generated CLI paths reset. Reopen Desktop normally. The shared backend is left running to preserve any ongoing work.\n');
}
