import fs from 'node:fs';
import path from 'node:path';
import { checkPrivate, privateDirectory, makePrivate } from './platform.mjs';
export { privateDirectory, processIdentity } from './platform.mjs';

export const configVariable = 'LAZYMIND_CODEX_DESKTOP_CONFIG';
export const kinds = ['codex_app', 'cua_repl'];

export function loadConfig(file = process.env[configVariable]) {
  if (!file || !path.isAbsolute(file)) throw new Error(`${configVariable} must name an absolute config file`);
  const info = checkPrivate(file);
  if (!info.isFile()) throw new Error('Runtime config must be a private owned file');
  const config = JSON.parse(fs.readFileSync(file, 'utf8'));
  if (![1, 2].includes(config.version)) throw new Error('Unsupported Desktop runtime config');
  for (const key of ['runtimeDir', 'bundleDir', 'cli', 'node', 'desktopExecutable', 'codexHome']) {
    if (typeof config[key] !== 'string' || !path.isAbsolute(config[key])) throw new Error(`Invalid ${key}`);
  }
  if (config.version === 2 && (!path.isAbsolute(config.helper ?? '') || !path.isAbsolute(config.launcher ?? ''))) throw new Error('Invalid native runtime entrypoint');
  for (const kind of kinds) {
    if (!config.plugins?.[kind]?.command || !Array.isArray(config.plugins[kind].args)) throw new Error(`Missing original ${kind} configuration`);
  }
  // macOS sockaddr_un.sun_path has 104 bytes, including the trailing NUL.
  if (process.platform === 'darwin' && Buffer.byteLength(path.join(config.runtimeDir, 'bridge-2147483647', 'codex_app.sock')) >= 104) throw new Error('Runtime socket path is too long');
  privateDirectory(config.runtimeDir);
  return { ...config, configFile: file };
}

export function atomicJSON(file, value) {
  const temporary = `${file}.${process.pid}.tmp`;
  fs.writeFileSync(temporary, JSON.stringify(value, null, 2) + '\n', { mode: 0o600 });
  makePrivate(temporary);
  fs.renameSync(temporary, file);
}

// Only operational fields are logged. Never log RPC bodies, tool results or identities.
export function logger(config, name) {
  const file = path.join(config.runtimeDir, `${name}.jsonl`);
  return (event, fields = {}) => {
    try {
      if (fs.existsSync(file) && fs.statSync(file).size > 2 * 1024 * 1024) fs.renameSync(file, `${file}.1`);
      fs.appendFileSync(file, JSON.stringify({ time: new Date().toISOString(), pid: process.pid, event, ...fields }) + '\n', { mode: 0o600 });
    } catch { /* A diagnostic write must not break the transport. */ }
  };
}
