import { describe, expect, it } from 'vitest';
import {
  resolveRuntimeMode,
} from '../../frontend/src/runtime/mode.ts';
import {
  resolveRuntimeFeatures,
} from '../../frontend/src/runtime/features.ts';
import {
  resolveApiBaseUrl,
  resolveApiUrl,
  resolveAuthServiceApiUrl,
  resolveCoreApiUrl,
} from '../../frontend/src/runtime/apiBase.ts';

describe('runtime mode facade', () => {
  it('defaults to cloud mode when unset or unknown', () => {
    expect(resolveRuntimeMode({})).toBe('cloud');
    expect(resolveRuntimeMode({ VITE_LAZYMIND_MODE: 'unknown' })).toBe('cloud');
  });

  it('accepts local and desktop modes explicitly', () => {
    expect(resolveRuntimeMode({ VITE_LAZYMIND_MODE: 'local' })).toBe('local');
    expect(resolveRuntimeMode({ VITE_LAZYMIND_MODE: 'desktop' })).toBe('desktop');
  });
});

describe('runtime feature facade', () => {
  it('keeps cloud features visible by default', () => {
    expect(resolveRuntimeFeatures({})).toMatchObject({
      hideEvo: false,
      hideRegister: false,
      hideCloudAdmin: false,
      localAutoLogin: false,
      useLocalGateway: false,
    });
  });

  it('enables local presentation defaults for local and desktop', () => {
    expect(resolveRuntimeFeatures({ VITE_LAZYMIND_MODE: 'local' })).toMatchObject({
      hideEvo: true,
      hideRegister: true,
      hideCloudAdmin: true,
      localAutoLogin: true,
      allowFolderPicker: false,
      allowOpenLogDir: false,
      useLocalGateway: true,
    });
    expect(resolveRuntimeFeatures({ VITE_LAZYMIND_MODE: 'desktop' })).toMatchObject({
      hideEvo: true,
      hideRegister: true,
      hideCloudAdmin: true,
      localAutoLogin: true,
      allowFolderPicker: true,
      allowOpenLogDir: true,
      useLocalGateway: true,
    });
  });

  it('lets VITE_HIDE_EVO explicitly override mode defaults', () => {
    expect(resolveRuntimeFeatures({ VITE_HIDE_EVO: 'true' }).hideEvo).toBe(true);
    expect(resolveRuntimeFeatures({
      VITE_LAZYMIND_MODE: 'local',
      VITE_HIDE_EVO: 'false',
    }).hideEvo).toBe(false);
  });
});

describe('runtime API base facade', () => {
  it('normalizes API base URL trailing slashes', () => {
    expect(resolveApiBaseUrl(
      { VITE_API_BASE_URL: 'http://127.0.0.1:8090///' },
      'http://localhost:5173',
    )).toBe('http://127.0.0.1:8090');
  });

  it('normalizes API paths with a single separator', () => {
    const env = { VITE_API_BASE_URL: 'http://127.0.0.1:8090/' };
    expect(resolveApiUrl('/api/healthz', env, '')).toBe('http://127.0.0.1:8090/api/healthz');
    expect(resolveApiUrl('api/healthz', env, '')).toBe('http://127.0.0.1:8090/api/healthz');
    expect(resolveCoreApiUrl('/temp/uploads:initUpload', env, '')).toBe(
      'http://127.0.0.1:8090/api/core/temp/uploads:initUpload',
    );
    expect(resolveAuthServiceApiUrl('auth/refresh', env, '')).toBe(
      'http://127.0.0.1:8090/api/authservice/auth/refresh',
    );
  });
});
