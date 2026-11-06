import assert from 'node:assert/strict';
import test from 'node:test';
import {mkdtemp, writeFile, readFile, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {fileURLToPath} from 'node:url';
import {chromium} from 'playwright';
import {inspectSlideQuality} from '../lib/slide_quality.mjs';

test('real DOM detects stretched text, SVG glyph width, malformed image fit, and overflow', async () => {
  const browser = await chromium.launch({headless:true});
  try {
    const page = await browser.newPage({viewport:{width:1600,height:900}});
    const svg = 'data:image/svg+xml;base64,' + Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect width="40" height="20" fill="red"/></svg>').toString('base64');
    await page.setContent(`<style>body{margin:0}.wrapper{width:1600px;height:900px;position:relative;font:24px Arial}p{width:max-content}</style>
      <div class="wrapper"><p id="wide" style="transform:scaleX(2);transform-origin:left">Natural width matters</p>
      <svg width="600" height="100" viewBox="0 0 200 100" preserveAspectRatio="none"><text id="svg-wide" x="0" y="40">M = 64</text></svg>
      <svg width="400" height="100"><text id="forced" x="0" y="40" textLength="350" lengthAdjust="spacingAndGlyphs">short</text></svg>
      <img id="image" src="${svg}" style="width:100px;height:100px">
      <p id="outside" style="position:absolute;top:910px">Outside</p></div>`);
    await page.locator('img').evaluate(img=>img.decode());
    const result = await page.evaluate(inspectSlideQuality);
    for (const code of ['NON_UNIFORM_TEXT_SCALE','FORCED_GLYPH_WIDTH','IMAGE_STRETCHED','TEXT_OUTSIDE_SLIDE']) {
      assert.ok(result.some(f=>f.code===code), code);
    }
    await page.locator('#wide').evaluate(n=>n.style.transform='scale(.8)');
    await page.locator('svg').first().evaluate(n=>n.setAttribute('preserveAspectRatio','xMidYMid meet'));
    await page.locator('#forced').evaluate(n=>{n.removeAttribute('textLength');n.removeAttribute('lengthAdjust');});
    await page.locator('#image').evaluate(n=>n.style.objectFit='contain');
    await page.locator('#outside').evaluate(n=>n.style.top='700px');
    assert.deepEqual(await page.evaluate(inspectSlideQuality), []);
    await page.locator('#image').evaluate(n=>n.style.transform='scaleX(1.5)');
    assert.ok((await page.evaluate(inspectSlideQuality)).some(f=>f.code==='IMAGE_STRETCHED'));
  } finally { await browser.close(); }
});

test('PDF exporter preserves previous output on quality failure and succeeds with natural text', async () => {
  const dir = await mkdtemp(path.join(tmpdir(),'ppt-pdf-quality-'));
  const run = promisify(execFile);
  const converter = fileURLToPath(new URL('../html_to_pdf.mjs',import.meta.url));
  const html = path.join(dir,'page_001.html');
  const output = path.join(dir,'result.pdf');
  const body = transform => `<!doctype html><html><head><style>body{margin:0}.wrapper{width:1600px;height:900px}h1{width:600px;transform-origin:top left;transform:${transform}}</style></head><body><div class="wrapper"><h1>Readable PDF text</h1></div></body></html>`;
  try {
    await writeFile(html,body('scaleX(1.6)'));
    await writeFile(output,'previous publication');
    const args=[converter,'--deck-dir',dir,'--pages-dir',dir,'--output-dir',dir,'--output','result.pdf','--batch','--force'];
    await assert.rejects(run(process.execPath,args),error=>error.stderr.includes('NON_UNIFORM_TEXT_SCALE'));
    assert.equal(await readFile(output,'utf8'),'previous publication');
    await writeFile(path.join(dir,'icon.svg'),'<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect width="40" height="20" fill="red"/></svg>');
    await writeFile(html,body('scale(1)').replace('</h1>','</h1><img src="icon.svg" style="width:80px;height:40px;object-fit:contain"><script>document.body.dataset.loaded="true";</script>'));
    await run(process.execPath,args);
    assert.equal((await readFile(output)).subarray(0,4).toString(),'%PDF');
  } finally { await rm(dir,{recursive:true,force:true}); }
});
