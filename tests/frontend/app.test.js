import { describe, expect, it } from 'vitest';
import {
  formRulesSource,
  frontendDockerfileSource,
  indexHtml,
  localComposeSource,
  loginSource,
  mainLayoutSource,
  mainEntry,
  routePaths,
  runtimeApiBaseSource,
  runtimeDesktopBridgeSource,
  runtimeFeaturesSource,
  runtimeModeSource,
  routerSource,
} from './setup.js';

describe('Vite entrypoint', () => {
  it('mounts the React app through the current module entry', () => {
    expect(indexHtml).toContain('<div id="app"></div>');
    expect(indexHtml).toContain('<script type="module" src="/src/main.tsx"></script>');
    expect(mainEntry).toContain('createRoot');
    expect(mainEntry).toContain('document.getElementById("app")');
    expect(mainEntry).toMatch(/root\.render\s*\(\s*<App\s*\/>\s*\)/);
  });
});

describe('router contract', () => {
  it('keeps public auth routes available', () => {
    expect(routePaths).toContain('/login');
    expect(routePaths).toContain('/register');
    expect(routePaths).toContain('/loginTransition');
  });

  it('keeps primary authenticated product routes available', () => {
    expect(routePaths).toContain('/');
    expect(routePaths).toContain('agent/chat');
    expect(routePaths).toContain('lib/knowledge');
    expect(routePaths).toContain('data-sources');
    expect(routePaths).toContain('model-providers');
    expect(routePaths).toContain('memory-management');
    expect(routePaths).toContain('self-evolution');
  });

  it('keeps admin routes available', () => {
    expect(routePaths).toContain('/admin');
    expect(routePaths).toContain('users');
    expect(routePaths).toContain('groups');
    expect(routePaths).toContain('groups/:id');
  });

  it('keeps fallback navigation wired to the app root', () => {
    expect(routerSource).toContain('<Route path="*" element={<Navigate to="/" replace />} />');
  });
});

describe('runtime facade contract', () => {
  it('keeps runtime facade modules present', () => {
    expect(runtimeModeSource).toContain('export type RuntimeMode');
    expect(runtimeFeaturesSource).toContain('export const runtimeFeatures');
    expect(runtimeApiBaseSource).toContain('export function getApiBaseUrl');
    expect(runtimeDesktopBridgeSource).toContain('export function openLogsDir');
    expect(runtimeDesktopBridgeSource).toContain('export function openDataDir');
    expect(runtimeDesktopBridgeSource).toContain('handler.call(bridge)');
    expect(runtimeDesktopBridgeSource).not.toContain('diagnostics');
    expect(runtimeDesktopBridgeSource).not.toContain('serviceStatus');
  });

  it('routes runtime mode checks through the facade', () => {
    expect(routerSource).toContain('runtimeFeatures.hideRegister');
    expect(routerSource).toContain('runtimeFeatures.hideCloudAdmin');
    expect(routerSource).toContain('runtimeFeatures.hideEvo');
    expect(mainLayoutSource).toContain('runtimeFeatures.hideEvo');
    expect(loginSource).toContain('runtimeFeatures.hideRegister');
    expect(mainLayoutSource).not.toContain('VITE_HIDE_EVO');
    expect(routerSource).not.toContain('VITE_HIDE_EVO');
    expect(loginSource).not.toContain('VITE_HIDE_EVO');
  });

  it('keeps frontend Docker build args available while local mode disables the frontend container', () => {
    expect(frontendDockerfileSource).toContain('ARG VITE_API_BASE_URL');
    expect(frontendDockerfileSource).toContain('ARG VITE_LAZYMIND_MODE');
    expect(frontendDockerfileSource).toContain('ARG VITE_HIDE_EVO');
    expect(localComposeSource).toMatch(/disabled_container_services:[\s\S]*-\s*frontend/);
    expect(localComposeSource).not.toMatch(/frontend:[\s\S]*VITE_LAZYMIND_MODE:\s*local/);
  });
});

describe('signin validation contract', () => {
  it('keeps username and password validators exported', () => {
    expect(formRulesSource).toContain('export const validateUsername');
    expect(formRulesSource).toContain('export const validatePassword');
    expect(formRulesSource).toContain('export const usernameRules');
    expect(formRulesSource).toContain('export const passwordRules');
  });

  it('keeps username and password regex definitions', () => {
    expect(formRulesSource).toMatch(/const\s+USERNAME_REGEX\s*=/);
    expect(formRulesSource).toMatch(/const\s+PASSWORD_REGEX\s*=/);
    expect(formRulesSource).toContain('USERNAME_MAX_LENGTH');
  });
});
