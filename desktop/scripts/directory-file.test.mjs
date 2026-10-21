import assert from 'node:assert/strict';
import test from 'node:test';
import { createRequire } from 'node:module';
const { revealDirectoryFile } = createRequire(import.meta.url)('../electron/src/directory-file.js');

for (const [platform, target] of [
  ['darwin', '/Users/test/资料/会议 纪要.md'],
  ['win32', 'C:\\Users\\test\\资料\\会议 纪要.md'],
  ['win32', '\\\\server\\share\\资料\\会议 纪要.md'],
]) {
  test(`reveals the original path through the system shell: ${target}`, async () => {
    let shown;
    const reference = { directoryId: 'directory', entryId: 'entry' };
    const result = await revealDirectoryFile(reference, {
      platform, hostname: 'host',
      lookup: async input => { assert.deepEqual(input, reference); return { path: target, namespace: `host:${platform === 'win32' ? 'nt' : 'posix'}` }; },
      realpath: async input => input,
      stat: async () => ({ isFile: () => true }),
      showItemInFolder: value => { shown = value; },
    });
    assert.equal(shown, target);
    assert.equal(result.path, target);
  });
}

test('rejects another host, unavailable references and changed paths without shell effects', async () => {
  let shown = false;
  const options = { platform: 'win32', hostname: 'host', realpath: async () => 'C:\\other.txt', stat: async () => ({ isFile: () => true }), showItemInFolder: () => { shown = true; } };
  for (const file of [
    { path: 'C:\\file.txt', namespace: 'other:nt' },
    { path: 'C:\\file.txt', namespace: 'host:nt', error_code: 'denied' },
    { path: 'C:\\file.txt', namespace: 'host:nt' },
  ]) {
    await assert.rejects(revealDirectoryFile({ directoryId: 'd', entryId: 'e' }, { ...options, lookup: async () => file }));
  }
  assert.equal(shown, false);
});
