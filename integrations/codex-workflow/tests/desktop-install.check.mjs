import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';
import { prepareInstallation, activateInstallation, disableInstallation } from '../desktop-runtime/installation.mjs';
import { disableActivation } from '../desktop-runtime/activation.mjs';
import { atomicJSON } from '../desktop-runtime/config.mjs';
import { makePrivate } from '../desktop-runtime/platform.mjs';
import { restoreGeneratedPaths } from '../desktop-runtime/restore-paths.mjs';
import { registerWorkflow, unregisterWorkflow, registrationPath } from '../desktop-runtime/workflow-registration.mjs';

function fixture() {
  const profile = '/fixture/profile', root = '/fixture/install';
  let current = null, running = false, failEnable = false, failPrepare = false;
  const configs = new Map(), fingerprints = new Map(), events = [];
  const ops = {
    registration: () => current && { installation: current, profile },
    config: file => { if (!configs.has(file)) throw new Error('unknown config'); return configs.get(file); },
    active: c => c.configFile === current,
    running: () => running,
    prepare: directory => {
      events.push('prepare');
      if (failPrepare) throw new Error('unsupported version');
      const c = { configFile: path.join(directory, 'runtime.json'), codexHome: profile, cliVersion: 'fixed', plugins: { native: 'original' } };
      configs.set(c.configFile, c); return c;
    },
    fingerprint: c => fingerprints.get(c.configFile),
    mark: (c, fingerprint) => fingerprints.set(c.configFile, fingerprint),
    removePrepared: directory => { events.push('remove prepared'); configs.delete(path.join(directory, 'runtime.json')); },
    enable: c => { events.push('enable '+c.configFile); if (failEnable === c.configFile) throw new Error('activation failed'); current = c.configFile; },
    disable: c => { events.push('disable '+c.configFile); assert.equal(current, c.configFile); current = null; },
  };
  const prepare = build => prepareInstallation({ root, profile, app: '/fixture/App.app', build }, ops);
  const activate = plan => activateInstallation(plan, profile, ops);
  return { profile, ops, events, configs, prepare, activate, current: () => current,
    setRunning: value => running = value, failEnable: file => failEnable = file, failPrepare: () => failPrepare = true };
}

test('first install activates only on commit; repeated install reuses the same release even while Desktop runs', () => {
  const f = fixture(), first = f.prepare('v1');
  assert.equal(f.current(), null);
  f.activate(first); f.setRunning(true);
  const repeated = f.prepare('v1'); f.activate(repeated);
  assert.equal(repeated.config, first.config);
  assert.equal(f.current(), first.config);
  assert.ok(!f.events.some(e => e.startsWith('disable')));
});

test('upgrade and disconnect refuse a running Desktop before changing existing activation', () => {
  const f = fixture(), old = f.prepare('v1'); f.activate(old); f.setRunning(true);
  assert.throws(() => f.prepare('v2'), /退出 Codex/);
  assert.throws(() => disableInstallation(f.profile, f.ops), /退出 Codex/);
  assert.equal(f.current(), old.config);
  assert.equal(f.configs.size, 1);
});

test('activation failure restores old release; successful upgrade and disable retain old files', () => {
  const f = fixture(), old = f.prepare('v1'); f.activate(old);
  const next = f.prepare('v2'); f.failEnable(next.config);
  assert.throws(() => f.activate(next), /activation failed/);
  assert.equal(f.current(), old.config);
  f.failEnable(false); f.activate(next);
  assert.equal(f.current(), next.config);
  disableInstallation(f.profile, f.ops);
  assert.equal(f.current(), null);
  assert.ok(f.configs.has(old.config) && f.configs.has(next.config));
});

test('failed preparation leaves old activation; concurrent changes and other profiles are rejected', () => {
  const f = fixture(), old = f.prepare('v1'); f.activate(old);
  f.failPrepare(); assert.throws(() => f.prepare('v2'), /unsupported/);
  assert.equal(f.current(), old.config);
  assert.throws(() => f.activate({ config: old.config, previous: null }), /changed during/);
  f.configs.get(old.config).codexHome = '/another-profile';
  assert.throws(() => disableInstallation(f.profile, f.ops), /another Codex profile/);
});

test('Desktop reopening after preparation blocks upgrade at activation too', () => {
  const f = fixture(), old = f.prepare('v1'); f.activate(old);
  const next = f.prepare('v2'); f.setRunning(true);
  assert.throws(() => f.activate(next), /退出 Codex/);
  assert.equal(f.current(), old.config);
});

for (const failure of ['set', 'restore', 'remove', 'unregister']) {
  test(`disconnect retries after ${failure} fails, retaining its recovery registration`, () => {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), 'lm-disconnect-'));
    makePrivate(root);
    const config = { configFile: path.join(root, 'runtime.json'), codexHome: root, runtimeDir: root,
      launcher: path.join(root, 'launch-adapter'), cli: path.join(root, 'codex') };
    const stateFile = path.join(root, 'activation.json'), generated = path.join(root, 'config.toml');
    const before = { CODEX_CLI_PATH: null };
    let environment = { CODEX_CLI_PATH: config.launcher }, failOnce = true, startup = true;
    const run = (name, action) => {
      if (failure === name && failOnce) { failOnce = false; throw new Error(`failed ${name}`); }
      action();
    };
    const ops = {
      registration: () => fs.existsSync(registrationPath(config)) ? JSON.parse(fs.readFileSync(registrationPath(config), 'utf8')) : null,
      config: () => config, active: () => fs.existsSync(stateFile), running: () => false,
      disable: () => disableActivation(config, stateFile, fs.existsSync(stateFile) ? JSON.parse(fs.readFileSync(stateFile, 'utf8')) : null, {
        running: () => false, validate: () => {}, stop: () => {},
        set: value => run('set', () => { environment = value; }),
        restore: () => run('restore', () => restoreGeneratedPaths(config, config.launcher)),
        remove: () => run('remove', () => { startup = false; }),
        unregister: () => run('unregister', () => unregisterWorkflow(config)),
      }),
    };
    try {
      atomicJSON(stateFile, { config: config.configFile, before });
      fs.writeFileSync(generated, `CODEX_CLI_PATH = ${JSON.stringify(config.launcher)}\n`, { mode: 0o600 });
      registerWorkflow(config);
      assert.throws(() => disableInstallation(root, ops), new RegExp(`failed ${failure}`));
      assert.ok(fs.existsSync(registrationPath(config)));
      disableInstallation(root, ops);
      assert.deepEqual(environment, before);
      assert.equal(startup, false);
      assert.equal(fs.readFileSync(generated, 'utf8'), `CODEX_CLI_PATH = ${JSON.stringify(config.cli)}\n`);
      assert.equal(fs.existsSync(stateFile), false);
      assert.equal(fs.existsSync(registrationPath(config)), false);
      disableInstallation(root, ops);
    } finally { fs.rmSync(root, { recursive: true, force: true }); }
  });
}
