import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { execFileSync } from 'node:child_process';
import { atomicJSON, privateDirectory, loadConfig } from './config.mjs';
import { desktopLayout } from './desktop-layout.mjs';
import { resolvePluginManifest } from './plugin-manifest.mjs';
import { runtimeLocation, launcherName, allocateLoopbackEndpoint, makePrivate } from './platform.mjs';

const { values } = parseArgs({ options: {
  output: { type: 'string' }, app: { type: 'string', default: '/Applications/ChatGPT.app' },
  'codex-home': { type: 'string', default: process.env.CODEX_HOME ?? path.join(os.homedir(), '.codex') },

  'runtime-dir': { type: 'string' }, 'desktop-plugin': { type: 'string' }, 'browser-plugin': { type: 'string' },
} });
if (!['darwin', 'win32'].includes(process.platform)) throw new Error('Desktop shared runtime supports macOS and Windows');
if (!values.output) throw new Error('Use --output <new private installation directory>; prepare does not enable shared mode');
const output = path.resolve(values.output);
const configFile = path.join(output, 'runtime.json');
if (fs.existsSync(output)) throw new Error('Output already exists; use a new directory to preserve any active installation');
const sourceBundle = path.dirname(fileURLToPath(import.meta.url));
for (const file of ['adapter.mjs', 'mcp-proxy.mjs', 'health.mjs', 'manage.mjs']) {
  fs.accessSync(path.join(sourceBundle, file), fs.constants.R_OK);
}
const app = fs.realpathSync(values.app);
const { resources, cli, node, desktopExecutable, desktopVersion } = desktopLayout(app);
const codexHome = fs.realpathSync(values['codex-home']);
const helper = process.env.LAZYMIND_RUNTIME_HELPER;
if (!helper || !path.isAbsolute(helper)) throw new Error('Install through the LazyMind CLI to provide the native runtime helper');
const cliVersion = execFileSync(cli, ['--version'], { encoding: 'utf8', timeout: 5000 }).trim().replace(/^codex-cli\s+/, '');
if (cliVersion !== '0.162.0-alpha.17.2') throw new Error(`CLI ${cliVersion} has not been validated for this runtime`);
execFileSync(node, ['-e', 'if (process.platform !== "win32" && typeof process.execve !== "function") process.exit(1)'], { timeout: 5000 });
if (process.platform === 'win32') {
 const help = execFileSync(cli, ['app-server', '--help'], { encoding: 'utf8', windowsHide: true, timeout: 10000 });
 if (!help.includes('--ws-token-file') || !help.includes('capability-token')) throw new Error('Bundled CLI lacks authenticated WebSocket support');
}

const sourceManifests = {};
function originalPlugin(kind, pluginName, explicit) {
  let file = explicit && path.resolve(explicit);
  if (!file) {
    const cache = path.join(codexHome, 'plugins/cache/openai-bundled', pluginName);
    // Match the plugin shipped with this App, never guess the newest cache entry.
    const manifest = path.join(resources, 'plugins/openai-bundled/plugins', pluginName, '.codex-plugin/plugin.json');
    const version = JSON.parse(fs.readFileSync(manifest, 'utf8')).version;
    if (typeof version !== 'string' || path.basename(version) !== version || version === '.' || version === '..') throw new Error(`Invalid bundled ${pluginName} version`);
    file = path.join(cache, version, '.mcp.json');
    if (!fs.existsSync(file)) throw new Error(`Open Codex once to initialize ${pluginName}, then reconnect LazyMind`);
  }
  const plugin = JSON.parse(fs.readFileSync(file, 'utf8')).mcpServers?.[kind];
  sourceManifests[kind] = file;
  return resolvePluginManifest(kind, plugin, file, node);
}

const plugins = {
  codex_app: originalPlugin('codex_app', 'codex-app-tools', values['desktop-plugin']),
  cua_repl: originalPlugin('cua_repl', 'unified-computer-use', values['browser-plugin']),
};
// A short, profile-specific location avoids macOS Unix socket path limits.
const key = crypto.createHash('sha256').update(output + '\0' + codexHome).digest('hex').slice(0, 16);
const runtimeDir = values['runtime-dir'] ? path.resolve(values['runtime-dir']) : runtimeLocation(output, key);
privateDirectory(path.dirname(runtimeDir));
privateDirectory(output);
const bundleDir = path.join(output, 'runtime');
privateDirectory(bundleDir);
for (const file of fs.readdirSync(sourceBundle).filter(file => file.endsWith('.mjs'))) {
  fs.copyFileSync(path.join(sourceBundle, file), path.join(bundleDir, file));
}
const launcherFile = path.join(output, launcherName());
const installedHelper = path.join(output, process.platform === 'win32' ? 'runtime-helper.exe' : 'runtime-helper');
fs.copyFileSync(helper, installedHelper);
makePrivate(installedHelper);
if (process.platform !== 'win32') fs.chmodSync(installedHelper, 0o700);
process.env.LAZYMIND_RUNTIME_HELPER = installedHelper;
const helperDigest = crypto.createHash('sha256').update(fs.readFileSync(installedHelper)).digest('hex');
let endpoint, tokenFile;
if (process.platform === 'win32') {
  privateDirectory(runtimeDir);
  endpoint = await allocateLoopbackEndpoint();
  tokenFile = path.join(runtimeDir, 'capability-token');
  fs.writeFileSync(tokenFile, crypto.randomBytes(32).toString('hex'), { flag: 'wx', mode: 0o600 });
  makePrivate(tokenFile);
  fs.copyFileSync(helper, launcherFile);
  makePrivate(launcherFile);
} else {
  const quote = text => "'" + text.replaceAll("'", "'\\''") + "'";
  const launcher = `#!/bin/sh\nexport LAZYMIND_CODEX_DESKTOP_CONFIG=${quote(configFile)}\nexport LAZYMIND_RUNTIME_HELPER=${quote(installedHelper)}\nexec ${quote(node)} ${quote(path.join(bundleDir, 'adapter.mjs'))} "$@"\n`;
  fs.writeFileSync(launcherFile, launcher, { mode: 0o700 });
}
atomicJSON(configFile, {
  version: 2, bundleDir, runtimeDir, cli, node, helper: installedHelper, launcher: launcherFile, codexHome, desktopExecutable,
  cliVersion, desktopVersion, plugins, sourceManifests, endpoint, tokenFile, helperDigest,
});
loadConfig(configFile);
process.stdout.write(JSON.stringify({ config: configFile, launcher: launcherFile, runtimeDir,
  next: [node, path.join(bundleDir, 'manage.mjs'), 'enable', '--config', configFile], enabled: false,
}, null, 2) + '\n');
