import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { chromium } from 'playwright';
import { assertOfflineIR, ensureDependencies, loadOfflineSlide } from '../lib/browser_setup.mjs';
import { collectGoogleFontFaces } from '../lib/font_embedding.mjs';

test('external rendering blocks page script, network and filesystem loads', async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'ppt-external-offline-'));
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ serviceWorkers: 'block' });
    const html = path.join(directory, 'slide.html');
    const secret = path.join(directory, 'secret.svg');
    await writeFile(secret, '<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>');
    await writeFile(html, `<html><body><script>window.untrustedRan=true</script>
      <h1 style="color:rgb(255,0,0)">Offline slide</h1>
      <img id="remote" src="http://127.0.0.1:9/secret"><img id="local" src="file://${secret}">
      <img id="inline" src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=">
      </body></html>`);
    const requests = [];
    page.on('requestfinished', request => requests.push(request.url()));
    await loadOfflineSlide(page, html);
    const result = await page.evaluate(() => ({
      ran: !!window.untrustedRan,
      widths: ['remote', 'local', 'inline'].map(id => document.getElementById(id).naturalWidth),
      color: getComputedStyle(document.querySelector('h1')).color,
    }));
    assert.deepEqual(result, { ran: false, widths: [0, 0, 1], color: 'rgb(255, 0, 0)' });
    assert.equal(requests.length, 0);
  } finally {
    await browser.close();
    await rm(directory, { recursive: true, force: true });
  }
});

test('external IR rejects local/remote image paths before the PPTX writer', () => {
  assert.throws(() => assertOfflineIR({ children: [{ src: '/etc/passwd' }] }), /inline/);
  assert.throws(() => assertOfflineIR({ styles: { backgroundImage: 'url("file:///etc/passwd")' } }), /inline/);
  assert.throws(() => assertOfflineIR({ styles: { backgroundImage: 'url("https://example.com/a.png")' } }), /inline/);
  assert.doesNotThrow(() => assertOfflineIR({ children: [{ src: 'data:image/png;base64,AA==' }] }));
});

test('external mode never installs renderer dependencies or fetches fonts', async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'ppt-external-deps-'));
  const oldOffline = process.env.LAZYMIND_PPT_EXTERNAL_OFFLINE;
  const oldDeps = process.env.LAZYMIND_PPT_EXPORT_DEPS;
  process.env.LAZYMIND_PPT_EXTERNAL_OFFLINE = '1';
  process.env.LAZYMIND_PPT_EXPORT_DEPS = directory;
  try {
    assert.throws(() => ensureDependencies(directory), /installed Node dependencies/);
    assert.deepEqual(await collectGoogleFontFaces([], { fetchImpl: () => assert.fail('must not fetch') }), []);
  } finally {
    if (oldOffline === undefined) delete process.env.LAZYMIND_PPT_EXTERNAL_OFFLINE;
    else process.env.LAZYMIND_PPT_EXTERNAL_OFFLINE = oldOffline;
    if (oldDeps === undefined) delete process.env.LAZYMIND_PPT_EXPORT_DEPS;
    else process.env.LAZYMIND_PPT_EXPORT_DEPS = oldDeps;
    await rm(directory, { recursive: true, force: true });
  }
});
