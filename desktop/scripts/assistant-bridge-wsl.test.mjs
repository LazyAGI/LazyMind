import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdtempSync, mkdirSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const launcher = path.join(repo, 'local/scripts/assistant-bridge-wsl.sh');
const posix = process.platform !== 'win32';

function fixture(t) {
  const parent = mkdtempSync(path.join(os.tmpdir(), 'lazymind-wsl-'));
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const root = path.join(parent, "O'Brien & $cash `cache` [资料]");
  const bin = path.join(root, 'probe-bin');
  mkdirSync(bin, { recursive: true });
  mkdirSync(path.join(root, 'local/scripts'), { recursive: true });
  mkdirSync(path.join(root, 'local/build/bin'), { recursive: true });
  copyFileSync(launcher, path.join(root, 'local/scripts/assistant-bridge-wsl.sh'));
  writeFileSync(path.join(root, 'local/scripts/assistant-bridge-win.ps1'), 'fixture');
  writeFileSync(path.join(root, 'local/build/bin/lazymind.exe'), 'fixture');
  writeFileSync(path.join(root, 'local/build/bin/lazymind'), 'stale Linux binary');
  const writeProbe = (name, body) => writeFileSync(path.join(bin, name), `#!${process.execPath}\n${body}`, { mode: 0o755 });
  writeProbe('wslpath', `
const fs = require('node:fs');
const input = process.argv.at(-1);
if(process.env.CASE_MODE === 'conversion-failure') process.exit(7);
if(process.env.CASE_MODE === 'empty-conversion') process.exit(0);
fs.appendFileSync(process.env.PATH_LOG, JSON.stringify(process.argv.slice(2))+'\\n');
console.log(process.env.WINDOWS_ROOT + input.slice(input.lastIndexOf('/local/')).replaceAll('/', String.fromCharCode(92)));
`);
  writeProbe('powershell.exe', `
const fs = require('node:fs');
fs.writeFileSync(process.env.PROBE_LOG, JSON.stringify({args:process.argv.slice(2),script:process.env.LAZYMIND_WSL_BRIDGE_SCRIPT,source:process.env.LAZYMIND_WSL_BRIDGE_SOURCE,action:process.env.LAZYMIND_WSL_BRIDGE_ACTION,wslenv:process.env.WSLENV}));
if(process.env.CASE_MODE === 'native-failure') process.exit(1);
// Simulate a Windows failure whose low eight bits are zero. No success receipt.
if(process.env.CASE_MODE === 'truncated-failure') {console.error('Windows launcher failed');process.exit(0);}
if(process.env.CASE_MODE === 'wrong-receipt') {console.log('LAZYMIND_ASSISTANT_BRIDGE_OK:other');process.exit(0);}
process.stdout.write('LAZYMIND_ASSISTANT_BRIDGE_OK:'+process.env.LAZYMIND_WSL_BRIDGE_ACTION+'\\r\\n');
`);
  const log = path.join(root, 'probe.json');
  const env = { ...process.env, OS: '', PATH: `${bin}:${process.env.PATH}`, PROBE_LOG: log,
    PATH_LOG: path.join(root, 'paths.jsonl'), WSLENV: 'EXISTING/p',
    WINDOWS_ROOT: "\\\\wsl.localhost\\Ubuntu\\home\\O'Brien & $cash `cache` [资料]", CASE_MODE: 'success' };
  return { root: realpathSync(root), bin, log, env };
}

for (const shell of ['/bin/sh', '/bin/bash']) {
  for (const kind of ['UNC', 'drive']) {
    for (const action of ['start', 'stop']) {
      test(`${shell} actual Make recipe preserves ${kind} paths for ${action}`, { skip: !posix }, t => {
        const f = fixture(t);
        if(kind === 'drive') f.env.WINDOWS_ROOT = "C:\\Users\\O'Brien & $cash `cache` [资料]";
        const result = spawnSync('make', ['--no-print-directory', '-f', path.join(repo, 'Makefile'),
          '-o', 'lazymind-cli-build', `SHELL=${shell}`, 'HOST_IS_WSL=1', `assistant-bridge-${action}`],
        { cwd:f.root, env:f.env, encoding:'utf8', timeout:10000 });
        assert.equal(result.status, 0, result.stderr);
        const called = JSON.parse(readFileSync(f.log, 'utf8'));
        assert.equal(called.script, f.env.WINDOWS_ROOT + '\\local\\scripts\\assistant-bridge-win.ps1');
        assert.equal(called.source, f.env.WINDOWS_ROOT + '\\local\\build\\bin\\lazymind.exe');
        assert.equal(called.action, action);
        assert.equal(called.wslenv, 'EXISTING/p:LAZYMIND_WSL_BRIDGE_SCRIPT/w:LAZYMIND_WSL_BRIDGE_SOURCE/w:LAZYMIND_WSL_BRIDGE_ACTION/w');
        assert.ok(called.args.includes('-Command'));
        assert.ok(!called.args.includes('-File'));
        assert.ok(!called.args.at(-1).includes(f.env.WINDOWS_ROOT), 'paths must not become PowerShell source');
        const paths = readFileSync(f.env.PATH_LOG,'utf8').trim().split('\n').map(JSON.parse);
        assert.deepEqual(paths, [['-w', path.join(f.root,'local/scripts/assistant-bridge-win.ps1')], ['-w',path.join(f.root,'local/build/bin/lazymind.exe')]]);
        assert.equal(existsSync(path.join(f.root,'local/build/bin/lazymind')), action !== 'start');
        assert.equal(result.stdout.includes('已启动'), action === 'start');
      });
    }
  }
}

for (const mode of ['native-failure', 'truncated-failure', 'wrong-receipt', 'conversion-failure', 'empty-conversion']) {
  for (const action of ['start','stop']) {
    test(`${action} propagates ${mode} without a success banner`, {skip:!posix}, t => {
      const f=fixture(t);f.env.CASE_MODE=mode;
      const result=spawnSync('make',['--no-print-directory','-f',path.join(repo,'Makefile'),'-o','lazymind-cli-build','HOST_IS_WSL=1',`assistant-bridge-${action}`],{cwd:f.root,env:f.env,encoding:'utf8',timeout:10000});
      assert.notEqual(result.status,0);
      assert.ok(!result.stdout.includes('已启动'));
      assert.ok(existsSync(path.join(f.root,'local/build/bin/lazymind')), 'failed start must not remove the old executable');
      if(mode.includes('conversion'))assert.ok(!existsSync(f.log),'invalid conversion must fail before invoking PowerShell');
    });
  }
}
