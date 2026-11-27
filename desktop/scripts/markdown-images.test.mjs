import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const { collectMarkdownImages } = require('../electron/src/markdown-images.js');
const { createDesktopBridge } = require('../electron/src/preload.js');

test('collects relative, encoded, absolute and file-URI images while preserving the original', async () => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'markdown-images-'));
  try {
    const doc = path.join(root, 'material.md');
    await fs.writeFile(doc, '# Original');
    const image = path.join(root, '图 1.png');
    await fs.writeFile(image, Buffer.from('image snapshot'));
    const result = await collectMarkdownImages(doc, ['图%201.png', image, new URL(`file://${image}`).href, 'missing.png']);
    assert.equal(result.images.length, 3);
    assert.equal(result.warnings.length, 1);
    assert.equal(Buffer.from(result.images[0].content_base64, 'base64').toString(), 'image snapshot');
    assert.equal(await fs.readFile(doc, 'utf8'), '# Original');
  } finally { await fs.rm(root, { recursive: true, force: true }); }
});

test('skips directories, non-images and symlinks outside the source directory', async () => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'markdown-images-roots-'));
  try {
    await fs.mkdir(path.join(root, 'docs'));
    const doc = path.join(root, 'docs', 'material.md');
    await fs.writeFile(doc, '# Original');
    await fs.writeFile(path.join(root, 'outside.png'), 'image');
    await fs.writeFile(path.join(root, 'docs', 'secret.txt'), 'private');
    if (process.platform !== 'win32') await fs.symlink(path.join(root, 'outside.png'), path.join(root, 'docs', 'linked.png'));
    const result = await collectMarkdownImages(doc, ['../outside.png', 'linked.png', 'secret.txt', '.']);
    assert.deepEqual(result.images, []);
    assert.equal(result.warnings.length, 4);
    const allowed = await collectMarkdownImages(doc, ['../outside.png'], [root]);
    assert.equal(allowed.images.length, 1);
  } finally { await fs.rm(root, { recursive: true, force: true }); }
});

test('preload derives the document path from the actual selected File', async () => {
  const file = {};
  let payload;
  const bridge = createDesktopBridge({ invoke: async (_channel, value) => { payload = value; return { images: [], warnings: [] }; } },
    { getPathForFile: actual => { assert.equal(actual, file); return '/chosen/material.md'; } });
  await bridge.collectMarkdownImages(file, ['figure.png']);
  assert.deepEqual(payload, { filename: '/chosen/material.md', references: ['figure.png'] });
});

for (const count of [3, 10]) {
  test(`collects ${count} slow images with a count-based budget`, async t => {
    const root = await fs.mkdtemp(path.join(os.tmpdir(), 'markdown-slow-'));
    let now = 0;
    const open = fs.open;
    try {
      const doc = path.join(root, 'material.md');
      const refs = Array.from({ length: count }, (_, i) => `${i}.png`);
      await fs.writeFile(doc, '# Original');
      for (const ref of refs) await fs.writeFile(path.join(root, ref), ref);
      t.mock.method(Date, 'now', () => now);
      t.mock.method(fs, 'open', async (...args) => { now += 4000; return open(...args); });
      const result = await collectMarkdownImages(doc, refs);
      assert.deepEqual(result.images.map(image => image.source), refs);
      assert.deepEqual(result.warnings, []);
    } finally { t.mock.restoreAll(); await fs.rm(root, { recursive: true, force: true }); }
  });
}

test('a stalled image does not discard earlier images or append a late result', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'markdown-timeout-'));
  const realpath = fs.realpath;
  let start, release;
  const started = new Promise(resolve => { start = resolve; });
  const held = new Promise(resolve => { release = resolve; });
  try {
    const doc = path.join(root, 'material.md');
    await fs.writeFile(doc, '# Body');
    for (const name of ['a.png', 'b.png', 'c.png']) await fs.writeFile(path.join(root, name), name);
    t.mock.timers.enable({ apis: ['setTimeout'] });
    t.mock.method(fs, 'realpath', async name => {
      if (name.endsWith('b.png')) { start(); await held; }
      return realpath(name);
    });
    const pending = collectMarkdownImages(doc, ['a.png', 'b.png', 'c.png']);
    await started;
    t.mock.timers.tick(5001);
    const result = await pending;
    assert.deepEqual(result.images.map(image => image.source), ['a.png', 'c.png']);
    assert.match(result.warnings.join(), /b.png/);
    release();
    await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(result.images.map(image => image.source), ['a.png', 'c.png']);
  } finally { release(); t.mock.restoreAll(); t.mock.timers.reset(); await fs.rm(root, { recursive: true, force: true }); }
});
