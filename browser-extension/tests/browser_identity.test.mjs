import test from 'node:test';
import assert from 'node:assert/strict';

import {detectBrowserIdentity} from '../src/browser_identity.js';

test('detects Microsoft Edge from user agent brands', () => {
  assert.deepEqual(detectBrowserIdentity({
    userAgentData: {
      brands: [
        {brand: 'Chromium', version: '140'},
        {brand: 'Microsoft Edge', version: '140'},
      ],
      platform: 'Windows',
    },
  }), {
    name: 'Microsoft Edge',
    version: '140',
    platform: 'Windows',
    deviceName: 'Windows Microsoft Edge',
  });
});

test('detects Edge from the legacy Edg user agent token', () => {
  const identity = detectBrowserIdentity({
    userAgent: 'Mozilla/5.0 Chrome/140.0.0.0 Safari/537.36 Edg/140.0.3485.54',
    platform: 'Win32',
  });

  assert.equal(identity.name, 'Microsoft Edge');
  assert.equal(identity.version, '140.0.3485.54');
  assert.equal(identity.deviceName, 'Win32 Microsoft Edge');
});

test('keeps Google Chrome and generic Chromium distinct', () => {
  assert.equal(detectBrowserIdentity({
    userAgentData: {brands: [{brand: 'Google Chrome', version: '140'}]},
  }).name, 'Google Chrome');
  assert.equal(detectBrowserIdentity({userAgent: 'Custom Browser'}).name, 'Chromium');
});
