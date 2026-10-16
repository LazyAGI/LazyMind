// OS mechanisms live here; installation and protocol code use these operations.
import fs from 'node:fs';
import path from 'node:path';
import net from 'node:net';
import { execFileSync, spawn } from 'node:child_process';

export function native(operation, request, helper = process.env.LAZYMIND_RUNTIME_HELPER) {
  if (!helper || !path.isAbsolute(helper)) throw new Error('Native runtime helper is required; reconnect LazyMind');
  return JSON.parse(execFileSync(helper, ['internal', 'codex-runtime', operation], {
    input: JSON.stringify(request), encoding: 'utf8', timeout: operation === 'ensure' ? 55000 : 10000,
    windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'],
  }));
}
export function checkPrivate(file) {
  const info = fs.lstatSync(file);
  if (info.isSymbolicLink()) throw new Error('Expected a private owned path');
  if (process.platform === 'win32') native('check-private', { path: file });
  else if (info.uid !== process.getuid() || (info.mode & 0o077)) throw new Error('Expected a private owned path');
  return info;
}
export function makePrivate(file) {
  if (process.platform === 'win32') native('make-private', { path: file });
  else fs.chmodSync(file, fs.statSync(file).isDirectory() ? 0o700 : 0o600);
}
export function privateDirectory(dir) {
  const exists = fs.existsSync(dir);
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  if (!exists) makePrivate(dir);
  if (!checkPrivate(dir).isDirectory()) throw new Error('Expected a private directory');
}
export function processInfo(pid) {
  if (!Number.isSafeInteger(pid) || pid < 1) return null;
  if (process.platform === 'win32') return native('process', { pid });
  try {
    const executable = execFileSync('/bin/ps', ['-p', String(pid), '-o', 'comm='], { encoding: 'utf8' }).trim();
    const ppid = Number(execFileSync('/bin/ps', ['-p', String(pid), '-o', 'ppid='], { encoding: 'utf8' }).trim());
    return { pid, ppid, executable };
  } catch { return null; }
}
export function processIdentity(pid) {
  if (!Number.isSafeInteger(pid) || pid < 1) return null;
  if (process.platform === 'win32') return processInfo(pid)?.identity ?? null;
  try { return execFileSync('/bin/ps', ['-p', String(pid), '-o', 'lstart=,comm='], {
    encoding: 'utf8', timeout: 2000, stdio: ['ignore', 'pipe', 'ignore'],
  }).trim() || null; } catch { return null; }
}
export function samePath(a, b) { return typeof a === 'string' && typeof b === 'string' &&
  (process.platform === 'win32' ? path.resolve(a).toLowerCase() === path.resolve(b).toLowerCase() : a === b); }
export function desktopParent(config) {
  let parent = processInfo(process.ppid);
  // Only accept our exact installed executable shim, with Desktop as its parent.
  if (process.platform === 'win32' && samePath(parent?.executable, config.launcher)) parent = processInfo(parent.ppid);
  return samePath(parent?.executable, config.desktopExecutable) ? parent : null;
}
export function desktopRunning(config) {
  if (process.platform === 'win32') return native('running', { path: config.desktopExecutable });
  return execFileSync('/bin/ps', ['-axo', 'comm='], { encoding: 'utf8', timeout: 5000 })
    .split('\n').some(line => line.trim() === config.desktopExecutable);
}
export async function delegateCLI(cli, args, env) {
  if (process.platform !== 'win32') process.execve(cli, [cli, ...args], env);
  const child = spawn(cli, args, { env, stdio: 'inherit', windowsHide: true });
  const forward = () => child.kill();
  process.once('SIGTERM', forward); process.once('SIGINT', forward);
  const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', code => resolve(code ?? 1)); });
  process.exit(code);
}
export function runtimeLocation(output, key) {
  return process.platform === 'win32' ? path.join(output, 'state') : `/private/tmp/lm-codex-${process.getuid()}/${key}`;
}
export function launcherName(platform = process.platform) { return platform === 'win32' ? 'launch-adapter.exe' : 'launch-adapter'; }
export async function allocateLoopbackEndpoint() {
  const server = net.createServer();
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  // Selection is not a reservation. Startup fails explicitly if this port is taken.
  return `ws://127.0.0.1:${port}`;
}
