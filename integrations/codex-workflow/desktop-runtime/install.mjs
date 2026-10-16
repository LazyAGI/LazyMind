import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { execFileSync } from 'node:child_process';
import { atomicJSON, loadConfig, privateDirectory } from './config.mjs';
import { checkPrivate, desktopRunning } from './platform.mjs';
import { prepareInstallation, activateInstallation, disableInstallation } from './installation.mjs';

try {
  const { positionals, values } = parseArgs({ allowPositionals: true, options: {
    root: { type: 'string' }, profile: { type: 'string' }, app: { type: 'string' }, build: { type: 'string' },
    'current-cli': { type: 'string' }, config: { type: 'string' }, previous: { type: 'string' },
  } });
  if (!['darwin', 'win32'].includes(process.platform)) throw new Error('Shared Desktop installation currently supports macOS and Windows');
  if (!values.profile || !path.isAbsolute(values.profile) || !values.root || !path.isAbsolute(values.root)) throw new Error('Absolute profile and installation root are required');
  const profile = fs.realpathSync(values.profile);
  const bundle = path.dirname(fileURLToPath(import.meta.url));
  const readPrivate = file => {
    const info = checkPrivate(file);
    if (!info.isFile()) throw new Error(`Expected a private owned file: ${file}`);
    return JSON.parse(fs.readFileSync(file, 'utf8'));
  };
  function run(entry, args) {
    try {
      return execFileSync(process.execPath, [path.join(bundle, entry), ...args], { encoding: 'utf8', timeout: 45000,
        stdio: ['ignore', 'pipe', 'pipe'], maxBuffer: 1024 * 1024 });
    } catch (error) {
      const detail = String(error.stderr || error.message).trim();
      throw new Error(detail.match(/^Error: (.+)$/m)?.[1] ?? detail);
    }
  }
  const ops = {
    registration: profile => {
      const file = path.join(profile, 'lazymind-shared-server.json');
      if (!fs.existsSync(file)) return null;
      const record = readPrivate(file);
      if (record.version !== 1 || record.profile !== profile || !path.isAbsolute(record.installation ?? '')) throw new Error('Invalid shared registration');
      return record;
    },
    config: loadConfig,
    active: config => {
      const file = path.join(path.dirname(config.configFile), 'activation.json');
      return fs.existsSync(file) && readPrivate(file).config === config.configFile;
    },
    running: desktopRunning,
    prepare: (directory, app, profile) => {
      privateDirectory(path.dirname(directory));
      run('prepare.mjs', ['--output', directory, '--app', app, '--codex-home', profile]);
      return loadConfig(path.join(directory, 'runtime.json'));
    },
    fingerprint: config => {
      const marker = path.join(path.dirname(config.configFile), 'installation.json');
      return fs.existsSync(marker) ? readPrivate(marker).fingerprint : null;
    },
    mark: (config, fingerprint) => atomicJSON(path.join(path.dirname(config.configFile), 'installation.json'), { version: 1, fingerprint }),
    removePrepared: directory => fs.rmSync(directory, { recursive: true, force: true }),
    enable: config => run('manage.mjs', ['enable', '--config', config.configFile]),
    disable: config => run('manage.mjs', ['disable', '--config', config.configFile, ...(values['current-cli'] ? ['--current-cli', values['current-cli']] : [])]),
  };
  if (positionals.length !== 1) throw new Error('Use prepare|activate|disable');
  let result = {};
  switch (positionals[0]) {
    case 'prepare':
      if (!values.app || !path.isAbsolute(values.app) || !values.build) throw new Error('App and build are required');
      result = prepareInstallation({ root: values.root, profile, app: values.app, build: values.build }, ops);
      break;
    case 'activate':
      if (!values.config || !path.isAbsolute(values.config)) throw new Error('Prepared configuration is required');
      activateInstallation({ config: values.config, previous: values.previous || null }, profile, ops);
      break;
    case 'disable': disableInstallation(profile, ops); break;
    default: throw new Error('Use prepare|activate|disable');
  }
  process.stdout.write(JSON.stringify(result) + '\n');

} catch (error) {
  process.stderr.write(error.message + '\n');
  process.exitCode = 1;
}
