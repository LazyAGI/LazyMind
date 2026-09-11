import assert from 'node:assert/strict';
import test from 'node:test';

import {assertCaptureAuthorized} from '../src/capture_authorization.js';

test('blocks page capture before the extension is paired', () => {
  assert.throws(
    () => assertCaptureAuthorized({connectionState: 'unpaired'}),
    (error) => error.code === 'PAIRING_REQUIRED' && error.message.includes('登录 LazyMind'),
  );
});

test('blocks page capture when the paired LazyMind connection is not authenticated', () => {
  assert.throws(
    () => assertCaptureAuthorized({
      deviceId: 'bd_test',
      deviceToken: 'secret',
      connectionState: 'unauthorized',
    }),
    (error) => error.code === 'LAZYMIND_CONNECTION_REQUIRED'
      && error.message.includes('登录授权已失效'),
  );
});

test('allows page capture only for a paired and authenticated connection', () => {
  assert.doesNotThrow(() => assertCaptureAuthorized({
    deviceId: 'bd_test',
    deviceToken: 'secret',
    connectionState: 'connected',
  }));
});
