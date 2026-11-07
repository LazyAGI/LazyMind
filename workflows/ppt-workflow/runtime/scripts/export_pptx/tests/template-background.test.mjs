import assert from 'node:assert/strict';
import test from 'node:test';
import {mkdtemp, mkdir, writeFile, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {chromium} from 'playwright';
import {inspectSlideQuality} from '../lib/slide_quality.mjs';

test('template background survives conflicting generated CSS and preserves source aspect in the browser', async () => {
  const dir = await mkdtemp(path.join(tmpdir(),'ppt-template-background-'));
  const library = fileURLToPath(new URL('../../../lib',import.meta.url));
  const browser = await chromium.launch({headless:true});
  try {
    await mkdir(path.join(dir,'pages'));
    await mkdir(path.join(dir,'images'));
    await writeFile(path.join(dir,'images/brand.svg'),'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 3"><rect width="4" height="3" fill="#ff0000"/></svg>');
    const html='<!doctype html><html><head><style>body{margin:0}.wrapper{width:1600px;height:900px;position:relative;background:white}#bg{background:green;width:100px;opacity:.2;transform:scaleX(2)}#ct{position:absolute;inset:0;background:blue}h1{position:absolute;left:300px;top:100px;font:40px Arial;margin:0}</style></head><body><div class="wrapper"><div id="bg"><div>Unwanted overlay</div></div><div id="ct"><h1>Approved content</h1></div></div></body></html>';
    const context={content_box_px:[200,0,1200,900],page:{source_page:2,background:{fill_mode:'stretch',image:{local_path:'images/brand.svg'}}}};
    await writeFile(path.join(dir,'input.json'),JSON.stringify({html,context}));
    const python = process.env.PYTHON_FOR_TEMPLATE_TEST || 'python3';
    await promisify(execFile)(python,['-c',
      'import sys,json,pathlib;sys.path.insert(0,sys.argv[1]);import template_html;p=pathlib.Path(sys.argv[2]);d=json.loads((p/"input.json").read_text());(p/"pages/page_001.html").write_text(template_html.apply_background(d["html"],d["context"]))',library,dir]);
    const page=await browser.newPage({viewport:{width:1600,height:900}});
    await page.goto(pathToFileURL(path.join(dir,'pages/page_001.html')).href);
    assert.equal(await page.locator('h1').innerText(),'Approved content');
    assert.equal(await page.locator('#bg>div').isVisible(),false);
    assert.deepEqual(await page.evaluate(inspectSlideQuality),[]);
    const rect=await page.locator('#bg').boundingBox();
    assert.deepEqual(rect,{x:200,y:0,width:1200,height:900});
    const image=(await page.screenshot()).toString('base64');
    const pixels=await page.evaluate(async data=>{
      const img=new Image();img.src='data:image/png;base64,'+data;await img.decode();
      const canvas=document.createElement('canvas');canvas.width=1600;canvas.height=900;
      const ctx=canvas.getContext('2d');ctx.drawImage(img,0,0);
      return [[100,10],[250,10],[1390,890],[1500,890]].map(([x,y])=>[...ctx.getImageData(x,y,1,1).data]);
    },image);
    assert.deepEqual(pixels,[[255,255,255,255],[255,0,0,255],[255,0,0,255],[255,255,255,255]]);
  } finally {await browser.close();await rm(dir,{recursive:true,force:true});}
});
