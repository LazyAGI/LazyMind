const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { localFileAction, openChatExternal } = require('../src/chat-link-actions');

test('opens and reveals the existing original file, propagating failures', async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'chat-links-'));
  const file = path.join(dir, '中文 report.docx');
  try {
    await fs.writeFile(file, 'example');
    const opened = [], revealed = [];
    const shell = { openPath: async p => { opened.push(p); return ''; }, showItemInFolder: p => revealed.push(p) };
    await localFileAction({ path: file, action: 'open' }, shell);
    await localFileAction({ path: file, action: 'reveal' }, shell);
    assert.deepEqual(opened, [file]); assert.deepEqual(revealed, [file]);
    await assert.rejects(localFileAction({ path: file, action: 'open' }, { openPath: async () => 'No associated application' }), /No associated/);
    await assert.rejects(localFileAction({ path: file + '.missing', action: 'open' }, shell), /ENOENT/);
    for (const source of ['relative.docx', 'https://example.com/a', 'file:///tmp/a']) {
      await assert.rejects(localFileAction({ path: source, action: 'open' }, shell), /Invalid/);
    }
  } finally { await fs.rm(dir, { recursive: true, force: true }); }
});

test('opens web links in the system browser and rejects other protocols', async () => {
  const opened = [];
  const shell = { openExternal: async url => opened.push(url) };
  await openChatExternal('https://example.com/page', shell);
  assert.deepEqual(opened, ['https://example.com/page']);
  for (const url of ['file:///tmp/a', 'javascript:alert(1)', 'codex://open']) await assert.rejects(openChatExternal(url, shell));
});
