import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import net from 'node:net';
import http from 'node:http';
import readline from 'node:readline';
import { PassThrough } from 'node:stream';
import { spawn, execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import { WebSocketServer } from 'ws';
import { forwardDesktop } from '../desktop-runtime/transport.mjs';
import { processIdentity, loadConfig } from '../desktop-runtime/config.mjs';
import { selectBridge } from '../desktop-runtime/bridge-registry.mjs';
import { restoreLauncherText } from '../desktop-runtime/restore-paths.mjs';
import { registerWorkflow, unregisterWorkflow, registrationPath } from '../desktop-runtime/workflow-registration.mjs';

const bundle = fileURLToPath(new URL('../dist/desktop', import.meta.url));
const require = createRequire(import.meta.url);
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check) {
  for (let i = 0; i < 200; i++) { const value = check(); if (value) return value; await delay(25); }
  throw new Error('Fixture timed out');
}
function fixture() {
  const dir = fs.mkdtempSync('/private/tmp/lm-runtime-');
  const config = { version: 1, runtimeDir: dir, bundleDir: bundle, cli: process.execPath, node: process.execPath,
    helper: process.env.LAZYMIND_TEST_HELPER, desktopExecutable: '/fixture/Desktop', codexHome: dir, cliVersion: 'fixture',
    plugins: Object.fromEntries(['codex_app', 'cua_repl'].map(kind => [kind, { command: process.execPath, args: [] }])),
  };
  const file = path.join(dir, 'runtime.json');
  const save = () => fs.writeFileSync(file, JSON.stringify(config), { mode: 0o600 });
  save();
  return { dir, config, file, save, env: { ...process.env, LAZYMIND_CODEX_DESKTOP_CONFIG: file }, remove: () => fs.rmSync(dir, { recursive: true, force: true }) };
}
function run(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, options); let stdout = '', stderr = '';
    child.stdout?.on('data', value => { stdout += value; });
    child.stderr?.on('data', value => { stderr += value; });
    child.once('error', reject);
    child.once('exit', code => code === 0 ? resolve(stdout) : reject(new Error(`${code}: ${stderr}`)));
  });
}

test('activation publishes one owned Workflow endpoint and rollback preserves another installation', () => {
  const f = fixture();
  const config = { ...f.config, configFile: f.file };
  try {
    registerWorkflow(config);
    assert.match(JSON.parse(fs.readFileSync(registrationPath(config), 'utf8')).app_server_url, /daemon.sock:\/rpc$/);
    assert.throws(() => unregisterWorkflow({ ...config, configFile: '/another/runtime.json' }), /Another installation/);
    unregisterWorkflow(config);
    assert.ok(!fs.existsSync(registrationPath(config)));
  } finally { f.remove(); }
});

test('Desktop transport preserves both directions, approvals and fragmented WebSocket messages', async () => {
  const f = fixture();
  const server = http.createServer();
  const ws = new WebSocketServer({ server });
  const received = [];
  let peer;
  ws.on('connection', socket => { peer = socket; socket.on('message', data => received.push(JSON.parse(data.toString()))); });
  await new Promise(resolve => server.listen(path.join(f.dir, 'rpc.sock'), resolve));
  const input = new PassThrough(), output = new PassThrough();
  const messages = [], failures = [];
  readline.createInterface({ input: output }).on('line', line => messages.push(JSON.parse(line)));
  let close;
  try {
    close = await forwardDesktop(path.join(f.dir, 'rpc.sock'), input, output, error => failures.push(error));
    const initialize = { id: 0, method: 'initialize', params: { clientInfo: { name: 'Desktop' } } };
    input.write(JSON.stringify(initialize) + '\n');
    await until(() => received.length);
    assert.deepEqual(received, [initialize]);
    const approval = { id: 'approval', method: 'item/commandExecution/requestApproval', params: { threadId: 'A', turnId: 'T' } };
    const encoded = JSON.stringify(approval);
    peer.send(encoded.slice(0, 15), { fin: false }); peer.send(encoded.slice(15), { fin: true });
    await until(() => messages.length);
    assert.deepEqual(messages, [approval]);
    const decision = { id: 'approval', result: { decision: 'decline' } };
    input.write(JSON.stringify(decision) + '\n');
    await until(() => received.length === 2);
    assert.deepEqual(received[1], decision);
    peer.close();
    await until(() => failures.length);
    assert.match(failures[0].message, /no replay/);
  } finally {
    close?.(); input.destroy(); output.destroy();
    for (const socket of ws.clients) socket.terminate();
    await new Promise(resolve => ws.close(resolve));
    await new Promise(resolve => server.close(resolve)); f.remove();
  }
});

test('built MCP proxy preserves context, reconnects and never replays an uncertain tool call', async () => {
  const f = fixture();
  const bridge = { pid: process.pid, ppid: process.ppid, identity: processIdentity(process.pid), parentIdentity: processIdentity(process.ppid),
    dir: path.join(f.dir, `bridge-${process.pid}`), group: 'fixture', created: 1 };
  fs.mkdirSync(bridge.dir, { mode: 0o700 });
  fs.writeFileSync(path.join(f.dir, `bridge-${process.pid}.json`), JSON.stringify(bridge), {mode: 0o600});
  fs.writeFileSync(path.join(f.dir, 'bridge-999999.json'), JSON.stringify({ ...bridge, pid: 999999 }), {mode: 0o600});
  assert.equal(selectBridge(f.dir).pid, process.pid);
  const seen = [], sockets = new Set(); let connections = 0;
  const server = net.createServer(socket => {
    connections++; sockets.add(socket); socket.on('close', () => sockets.delete(socket));
    readline.createInterface({ input: socket }).on('line', line => {
      const message = JSON.parse(line); seen.push(message);
      if (message.params?.name === 'drop') { socket.destroy(); return; }
      if (message.id !== undefined && message.method) socket.write(JSON.stringify({ jsonrpc: '2.0', id: message.id, result: { meta: message.params?._meta } }) + '\n');
    });
  });
  await new Promise(resolve => server.listen(path.join(bridge.dir, 'codex_app.sock'), resolve));
  const child = spawn(process.execPath, [path.join(bundle, 'mcp-proxy.mjs'), 'codex_app'], { env: f.env, stdio: ['pipe', 'pipe', 'inherit'] });
  const exited = new Promise(resolve => child.once('exit', resolve));
  const replies = [];
  readline.createInterface({ input: child.stdout }).on('line', line => replies.push(JSON.parse(line)));
  const send = message => child.stdin.write(JSON.stringify(message) + '\n');
  const get = id => until(() => replies.find(message => message.id === id));
  try {
    send({ id: 1, method: 'initialize', params: { protocolVersion: '2024-11-05', capabilities: {}, clientInfo: { name: 'fixture', version: '1' } } });
    await get(1); send({ method: 'notifications/initialized' });
    const meta = { 'openai/threadId': 'A', 'openai/turnId': 'T', 'x-codex-turn-metadata': 'opaque上下文' };
    send({ id: 2, method: 'tools/call', params: { name: 'read', _meta: meta } });
    assert.deepEqual((await get(2)).result.meta, meta);
    send({ id: 3, method: 'tools/call', params: { name: 'drop', _meta: meta } });
    assert.match((await get(3)).error.message, /outcome unknown/);
    send({ id: 'lost-server-request', result: { decision: 'decline' } });
    send({ id: 4, method: 'tools/call', params: { name: 'read', _meta: { threadId: 'B' } } });
    assert.equal((await get(4)).result.meta.threadId, 'B');
    assert.equal(connections, 2);
    assert.equal(seen.filter(message => message.params?.name === 'drop').length, 1);
    assert.equal(seen.filter(message => message.method === 'initialize').length, 2);
    assert.ok(!seen.some(message => message.id === 'lost-server-request'));
  } finally {
    child.stdin.end(); await exited;
    for (const socket of sockets) socket.destroy();
    await new Promise(resolve => server.close(resolve)); f.remove();
  }
});

test('three concurrent coordinator requests create one owned fixture backend and preserve unknown endpoints', { skip: process.platform !== 'darwin' }, async () => {
  const f = fixture();
  const cli = path.join(f.dir, 'fixture-cli.mjs');
  fs.writeFileSync(cli, `#!${process.execPath}\nimport http from 'node:http';\nimport {WebSocketServer} from ${JSON.stringify(new URL('wrapper.mjs', 'file://' + path.dirname(require.resolve('ws')) + '/').href)};
const server=http.createServer();const ws=new WebSocketServer({server});
ws.on('connection',s=>s.on('message',raw=>{const m=JSON.parse(raw);if(m.method==='initialize')s.send(JSON.stringify({id:m.id,result:{userAgent:'fixture',codexHome:process.env.CODEX_HOME}}))}));
process.umask(0o077);
server.listen(process.argv[process.argv.indexOf('--listen')+1].slice(7),()=>console.log('LISTENING'));
process.on('SIGTERM',()=>{for(const s of ws.clients)s.terminate();server.close(()=>process.exit(0))});
`, { mode: 0o700 });
  f.config.cli = cli; f.save();
  let backend;
  try {
    assert.ok(f.config.helper, 'Set LAZYMIND_TEST_HELPER to the freshly built LazyMind CLI');
    const coordinate = () => new Promise((resolve,reject) => {
      const child = spawn(f.config.helper, ['internal','codex-runtime','ensure'], {env:f.env,stdio:['pipe','pipe','pipe']});
      let out='',err=''; child.stdout.on('data',v=>out+=v); child.stderr.on('data',v=>err+=v); child.once('error',reject); child.once('exit',code=>code===0?resolve(out):reject(new Error(err)));
      child.stdin.end(JSON.stringify({path:f.file}));
    });
    const results = await Promise.all([coordinate(), coordinate(), coordinate()]);
    backend = JSON.parse(results[0]).process;
    assert.equal(new Set(results.map(result => JSON.parse(result).process.pid)).size, 1);
    const events = fs.readFileSync(path.join(f.dir, 'coordinator.jsonl'), 'utf8').trim().split('\n').map(JSON.parse);
    assert.equal(events.filter(event => event.event === 'spawn').length, 1);
    assert.equal(events.filter(event => event.event === 'reuse').length, 2);
    assert.ok(backend.identity);
    // An unknown endpoint is never deleted, even after the recorded process exits.
    process.kill(backend.pid, 'SIGTERM');
    await until(() => { try { process.kill(backend.pid, 0); return false; } catch { return true; } });
    fs.writeFileSync(path.join(f.dir, 'daemon.sock'), 'unrelated');
    await assert.rejects(coordinate(), /neither Unix socket/);
    assert.equal(fs.readFileSync(path.join(f.dir, 'daemon.sock'), 'utf8'), 'unrelated');
  } finally {
    if (!backend && fs.existsSync(path.join(f.dir, 'backend.json'))) backend = JSON.parse(fs.readFileSync(path.join(f.dir, 'backend.json'), 'utf8')).process;
    if (backend) { try { process.kill(backend.pid, 'SIGTERM'); } catch {} }
    f.remove();
  }
});

test('rollback repairs only the owned generated CLI value, preserving concurrent user edits', () => {
  const launcher = '/private/path with spaces/launch-adapter', cli = '/Applications/ChatGPT.app/codex';
  const manifest = JSON.stringify({ mcpServers: { cua_repl: { env: { CODEX_CLI_PATH: launcher, USER_EDIT: 'keep' } } }, notes: launcher });
  const restored = JSON.parse(restoreLauncherText(manifest, launcher, cli));
  assert.equal(restored.mcpServers.cua_repl.env.CODEX_CLI_PATH, cli);
  assert.equal(restored.mcpServers.cua_repl.env.USER_EDIT, 'keep');
  assert.equal(restored.notes, launcher);
  assert.equal(restoreLauncherText('CODEX_CLI_PATH = "/another/cli"\n', launcher, cli), 'CODEX_CLI_PATH = "/another/cli"\n');
  assert.equal(restoreLauncherText(`CODEX_CLI_PATH = ${JSON.stringify(launcher)} # keep\n`, launcher, cli), `CODEX_CLI_PATH = ${JSON.stringify(cli)} # keep\n`);
});

test('auxiliary CLI calls delegate unchanged without starting a backend; config rejects unsafe permissions', async () => {
  const f = fixture();
  try {
    const actual = await run(process.execPath, [path.join(bundle, 'adapter.mjs'), '--version'], { env: f.env });
    assert.equal(actual.trim(), process.version);
    assert.ok(!fs.existsSync(path.join(f.dir, 'backend.json')));
    fs.chmodSync(f.file, 0o644);
    assert.throws(() => loadConfig(f.file), /private owned/);
  } finally { f.remove(); }
});

test('prepared macOS installation ships a working native helper and launcher without Python or activation', {skip:process.platform!=='darwin'}, async()=>{
 const f=fixture();
 try{
  assert.ok(f.config.helper,'Set LAZYMIND_TEST_HELPER to the freshly built CLI');
  const app=path.join(f.dir,"Fixture App's.app"),resources=path.join(app,'Contents/Resources');
  const cli=path.join(resources,'codex-cli/CodexCLI.app/Contents/MacOS/codex'),node=path.join(resources,'cua_node/bin/node');
  for(const dir of [path.dirname(cli),path.dirname(node),path.join(app,'Contents/MacOS')])fs.mkdirSync(dir,{recursive:true,mode:0o700});
  fs.writeFileSync(cli,'#!/bin/sh\nprintf "codex-cli 0.162.0-alpha.17.2\\n"\n',{mode:0o700});fs.symlinkSync(process.execPath,node);
  fs.writeFileSync(path.join(app,'Contents/Info.plist'),'<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>Fixture</string><key>CFBundleShortVersionString</key><string>fixture</string></dict></plist>');
  fs.writeFileSync(path.join(f.dir,'server.mjs'),'');fs.writeFileSync(path.join(f.dir,'browser.mjs'),'');
  const desktop=path.join(f.dir,'desktop.json'),browser=path.join(f.dir,'browser.json');
  fs.writeFileSync(desktop,JSON.stringify({mcpServers:{codex_app:{command:node,args:[],cwd:f.dir}}}));
  fs.writeFileSync(browser,JSON.stringify({mcpServers:{cua_repl:{command:node,args:[path.join(f.dir,'browser.mjs')],cwd:f.dir}}}));
  const output=path.join(f.dir,"install's folder"),state=path.join(f.dir,'state');
  const result=JSON.parse(await run(process.execPath,[path.join(bundle,'prepare.mjs'),'--output',output,'--app',app,'--codex-home',f.dir,'--runtime-dir',state,'--desktop-plugin',desktop,'--browser-plugin',browser],
   {env:{...f.env,LAZYMIND_RUNTIME_HELPER:f.config.helper}}));
  const config=JSON.parse(fs.readFileSync(result.config,'utf8'));
  assert.equal(config.python,undefined);assert.equal(config.version,2);assert.match(config.helperDigest,/^[a-f0-9]{64}$/);
  assert.equal((await run(result.launcher,['--version'],{env:f.env})).trim(),'codex-cli 0.162.0-alpha.17.2');
  assert.equal(fs.existsSync(path.join(output,'activation.json')),false);assert.equal(fs.existsSync(path.join(state,'backend.json')),false);
 }finally{f.remove()}
});
