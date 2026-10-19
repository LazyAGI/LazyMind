import fs from 'node:fs';
import path from 'node:path';
import { atomicJSON } from './config.mjs';
import { checkPrivate } from './platform.mjs';
import { runtimeEndpoint } from './endpoint.mjs';

export function workflowRegistration(config) {
  return { version: 1, profile: config.codexHome, installation: config.configFile,
    app_server_url: runtimeEndpoint(config), ...(config.tokenFile ? { auth_token_file: config.tokenFile } : {}) };
}

export function registrationPath(config) { return path.join(config.codexHome, 'lazymind-shared-server.json'); }

function checkOwner(config) {
  const file = registrationPath(config);
  let info;
  try { info = checkPrivate(file); } catch (error) { if (error.code === 'ENOENT') return false; throw error; }
  if (!info.isFile()) throw new Error('Unsafe Workflow shared-server registration');
  const current = JSON.parse(fs.readFileSync(file, 'utf8'));
  if (current.installation !== config.configFile || current.profile !== config.codexHome) throw new Error('Another installation owns this profile shared-server registration');
  return true;
}

export function registerWorkflow(config) {
  checkOwner(config);
  atomicJSON(registrationPath(config), workflowRegistration(config));
}

export function unregisterWorkflow(config) {
  if (checkOwner(config)) fs.unlinkSync(registrationPath(config));
}
