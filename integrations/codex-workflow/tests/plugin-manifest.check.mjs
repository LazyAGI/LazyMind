import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { resolvePluginManifest } from '../desktop-runtime/plugin-manifest.mjs';

test('Windows desktop wrapper normalizes only the known command and preserves plugin policy', t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'lm-plugin-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const cwd = path.join(root, 'plugin');
  const node = path.join(root, 'bundled', 'node.exe');
  for (const file of [node, path.join(cwd, 'server.mjs'), path.join(cwd, 'scripts/launch_codex_app_tools_mcp.cmd')]) {
    fs.mkdirSync(path.dirname(file), { recursive: true }); fs.writeFileSync(file, 'fixture', { mode: 0o700 });
  }
  const plugin = { command: 'cmd.exe', args: ['/d', '/s', '/c', 'call', './scripts/launch_codex_app_tools_mcp.cmd', './server.mjs'], cwd,
    env: { KEEP: 'yes', CODEX_CLI_PATH: 'old' }, approval_policy: 'ask' };
  const resolve = value => resolvePluginManifest('codex_app', value, path.join(cwd, '.mcp.json'), node, { platform: 'win32' });
  const result = resolve(plugin);
  assert.equal(result.command, node);
  assert.deepEqual(result.args, [path.join(cwd, 'server.mjs')]);
  assert.deepEqual(result.env, { KEEP: 'yes' });
  assert.equal(result.approval_policy, 'ask');
  assert.throws(() => resolve({ ...plugin, args: [...plugin.args, '&', 'anything'] }), /Unsupported/);
  assert.throws(() => resolve({ ...plugin, command: 'powershell.exe' }), /resolved/);
  assert.throws(() => resolvePluginManifest('codex_app', plugin, path.join(cwd, '.mcp.json'), node, { platform: 'darwin' }), /resolved/);
  assert.equal(resolvePluginManifest('codex_app', { ...plugin, command: node, args: ['./server.mjs'] }, path.join(cwd, '.mcp.json'), node, { platform: 'darwin' }).command, node);
});

test('CUA accepts a complete managed runtime but rejects PATH, mixed runtimes and unrelated Node', t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'lm-cua-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const local = path.join(root, 'Local');
  const bundled = path.join(root, 'bundled', 'node.exe');
  const base = path.join(local, 'OpenAI', 'Codex', 'runtimes', 'cua_node');
  const node = path.join(base, 'runtime-one', 'bin', 'node.exe');
  const entry = path.join(base, 'runtime-one', 'lib', 'server.mjs');
  const other = path.join(base, 'runtime-two', 'server.mjs');
  const foreign = path.join(root, 'other', 'node.exe');
  for (const file of [bundled, node, entry, other, foreign]) {
    fs.mkdirSync(path.dirname(file), { recursive: true }); fs.writeFileSync(file, 'fixture', { mode: 0o700 });
  }
  const plugin = { command: node, args: [entry], cwd: root };
  const options = { platform: 'win32', localAppDataRoots: [path.join(root, 'redirected'), local] };
  const resolve = value => resolvePluginManifest('cua_repl', value, path.join(root, '.mcp.json'), bundled, options);
  assert.equal(resolve(plugin).command, node);
  assert.equal(resolve({ ...plugin, command: bundled }).command, bundled);
  assert.throws(() => resolve({ ...plugin, command: foreign }), /complete Codex-managed/);
  assert.throws(() => resolve({ ...plugin, args: [other] }), /complete Codex-managed/);
  assert.throws(() => resolve({ ...plugin, command: 'node.exe' }), /resolved/);
  assert.throws(() => resolve({ ...plugin, args: ['relative.mjs'] }), /not resolved/);
  assert.throws(() => resolvePluginManifest('cua_repl', plugin, path.join(root, '.mcp.json'), bundled, { ...options, platform: 'darwin' }), /complete Codex-managed/);
  fs.rmSync(node);
  assert.throws(() => resolve(plugin));
});
