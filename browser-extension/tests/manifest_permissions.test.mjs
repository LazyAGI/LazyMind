import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

const manifest = JSON.parse(await readFile(new URL('../manifest.json', import.meta.url), 'utf8'));

test('supports the Chrome version used by the desktop acceptance environment', () => {
  assert.equal(manifest.minimum_chrome_version, '124');
});

test('requires an explicit optional host grant before reading any page', () => {
  assert.equal(manifest.permissions.includes('activeTab'), false);
  assert.deepEqual(manifest.host_permissions || [], []);
  assert.deepEqual(manifest.optional_host_permissions, ['http://*/*', 'https://*/*']);
});
