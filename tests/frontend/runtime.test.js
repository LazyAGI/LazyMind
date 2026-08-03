import { describe, expect, it } from 'vitest';
import {
  isLocalLikeRuntimeMode,
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

  it('normalizes configured mode values before resolving them', () => {
    expect(resolveRuntimeMode({ VITE_LAZYMIND_MODE: ' LOCAL ' })).toBe('local');
    expect(resolveRuntimeMode({ VITE_LAZYMIND_MODE: 'Desktop' })).toBe('desktop');
  });

  it.each([
    ['cloud', false],
    ['local', true],
    ['desktop', true],
  ])('classifies %s local-like behavior', (mode, expected) => {
    expect(isLocalLikeRuntimeMode(mode)).toBe(expected);
  });
});

describe('runtime feature facade', () => {
  it('keeps cloud features visible by default', () => {
    expect(resolveRuntimeFeatures({})).toMatchObject({
      hideEvo: false,
      hideRegister: false,
      hideCloudAdmin: false,
      localLikeAutoLogin: false,
      useLocalGateway: false,
    });
  });

  it('enables local presentation defaults for local and desktop', () => {
    expect(resolveRuntimeFeatures({ VITE_LAZYMIND_MODE: 'local' })).toMatchObject({
      hideEvo: true,
      hideRegister: true,
      hideCloudAdmin: true,
      localLikeAutoLogin: true,
      allowFolderPicker: false,
      allowOpenLogDir: false,
      useLocalGateway: true,
    });
    expect(resolveRuntimeFeatures({ VITE_LAZYMIND_MODE: 'desktop' })).toMatchObject({
      hideEvo: true,
      hideRegister: true,
      hideCloudAdmin: true,
      localLikeAutoLogin: true,
      allowFolderPicker: true,
      allowOpenLogDir: true,
      useLocalGateway: true,
    });
  });

  it.each([
    ['local', false, false],
    ['desktop', true, true],
  ])(
    'keeps shared and shell-only capabilities distinct in %s mode',
    (mode, allowFolderPicker, allowOpenLogDir) => {
      expect(resolveRuntimeFeatures({ VITE_LAZYMIND_MODE: mode })).toEqual({
        hideEvo: true,
        hideRegister: true,
        hideCloudAdmin: true,
        localLikeAutoLogin: true,
        hideLocalUserControls: true,
        hideUserGroupSurfaces: true,
        allowFolderPicker,
        allowOpenLogDir,
        useLocalGateway: true,
      });
    },
  );

  it('lets VITE_HIDE_EVO explicitly override mode defaults', () => {
    expect(resolveRuntimeFeatures({ VITE_HIDE_EVO: 'true' }).hideEvo).toBe(true);
    expect(resolveRuntimeFeatures({
      VITE_LAZYMIND_MODE: 'local',
      VITE_HIDE_EVO: 'false',
    }).hideEvo).toBe(false);
  });

  it.each([
    ['1', true],
    ['yes', true],
    ['on', true],
    ['0', false],
    ['no', false],
    ['off', false],
  ])('parses VITE_HIDE_EVO=%s as %s', (value, expected) => {
    expect(resolveRuntimeFeatures({ VITE_HIDE_EVO: value }).hideEvo).toBe(expected);
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

  it.each(['local', 'desktop'])(
    'keeps %s traffic on the single local gateway origin',
    () => {
      const env = { VITE_API_BASE_URL: 'http://127.0.0.1:8090' };
      expect(resolveAuthServiceApiUrl('/auth/login', env, '')).toBe(
        'http://127.0.0.1:8090/api/authservice/auth/login',
      );
      expect(resolveCoreApiUrl('/healthz', env, '')).toBe(
        'http://127.0.0.1:8090/api/core/healthz',
      );
    },
  );
});
