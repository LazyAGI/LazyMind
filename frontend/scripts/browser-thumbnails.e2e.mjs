// PLAYWRIGHT_MODULE and CHROME_PATH may point to an existing local installation.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { createRequire } from 'node:module';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
const root = process.env.FRONTEND_ROOT || path.resolve(import.meta.dirname, '..');
const require = createRequire(path.join(root, 'package.json'));
const { build } = createRequire(require.resolve('vite'))('esbuild');
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const ppt = path.join(root, 'src/modules/chat/components/WorkflowPanel/ppt');
const bundle = await build({
  stdin: { contents: `import React from 'react'; import {createRoot} from 'react-dom/client';
    import {SlideThumb} from ${JSON.stringify(path.join(ppt, 'SlideThumb.tsx'))};
    import {captureHtmlSlidePng} from ${JSON.stringify(path.join(ppt, 'exportHtmlToPptx.ts'))};
    const root=createRoot(document.getElementById('root'));
    window.fixture={capture:captureHtmlSlidePng,show:(slots,sessionId)=>root.render(React.createElement('div',{},slots.map((slot,i)=>React.createElement(SlideThumb,{key:i,slot,sessionId}))))};`, resolveDir: root, loader: 'tsx' },
  bundle: true, write: false, format: 'iife', platform: 'browser', jsx: 'automatic',
  alias: { '@': path.join(root, 'src') },
  plugins: [{ name: 'fixtures', setup(build) {
    build.onResolve({ filter: /knowledge\/utils\/imageUrl$|^react-i18next$|echarts\.min\.js\?url$/ }, args => ({path:args.path,namespace:'fixture'}));
    build.onLoad({filter:/.*/,namespace:'fixture'}, args => ({loader:'js',contents:
      args.path==='react-i18next' ? 'export const useTranslation=()=>({t:k=>k});' :
      args.path.includes('echarts.min.js') ? 'export default "/echarts.min.js";' :
      'export const resolveCoreAssetUrl=p=>new URL(p,location.origin).href; export const resolveMarkdownImageUrlAsync=async p=>resolveCoreAssetUrl(p); export const isExpiredSignedUrl=()=>false;'}));
  }}],
});
const svg='<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><rect width="100" height="100" fill="#e02030"/></svg>';
const echarts=await readFile(require.resolve('echarts/dist/echarts.min.js'));
let browser, childOrigin, retryRequests=0;
const handler=(req,res)=>{
  res.setHeader('Cache-Control','no-store');
  if(req.url==='/bundle.js'){res.setHeader('Content-Type','text/javascript');res.end(bundle.outputFiles[0].text);}
  else if(req.url==='/echarts.min.js'){res.setHeader('Content-Type','text/javascript');res.end(echarts);}
  else if(req.url.startsWith('/static-files/')){
    if(req.url.includes('retry')&&++retryRequests===1){res.writeHead(503);res.end('temporary');return;}
    res.setHeader('Content-Type','image/svg+xml');res.end(svg);
  } else {res.setHeader('Content-Type','text/html');res.end(req.url==='/parent'
    ? `<iframe src="${childOrigin}/frame" style="width:100%;height:800px"></iframe>`
    : '<div id="root"></div><script src="/bundle.js"></script>');}
};
const parent=createServer(handler),child=createServer(handler);
const listen=server=>new Promise(r=>server.listen(0,'127.0.0.1',r));
const slot=(html,i=0)=>({slot_id:'preview_html',slot:'preview_html',list_index:i,sort_order:i,revision:1,artifact_value:{text:html}});
const html=body=>`<!doctype html><html><head><style>*{box-sizing:border-box}body{margin:0}.wrapper{width:1600px;height:900px;background:#f7f3ee;color:#35223f}h1{margin:0;padding:50px}</style></head><body><div class="wrapper">${body}</div></body></html>`;
const imageHTML=html('<h1>Image and SVG 中文</h1><img src="/static-files/image.svg" width="200" height="200"><svg width="300" height="300"><rect width="300" height="300" fill="#65a5a0"/></svg>');
const chartHTML=renderer=>html(`<h1>ECharts ${renderer}</h1><div id="chart_1" style="width:800px;height:500px"></div><script>window.onload=()=>{const c=echarts.init(document.getElementById('chart_1'),null,{renderer:'${renderer}'});c.setOption({animation:false,xAxis:{data:['A','B']},yAxis:{},series:[{type:'bar',data:[10,30]}]});window.__pptxChartsReady=1;};</script>`);
try {
  await Promise.all([listen(parent),listen(child)]);
  childOrigin=`http://127.0.0.1:${child.address().port}`;
  browser=await chromium.launch({executablePath:process.env.CHROME_PATH||'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true});
  const page=await browser.newPage();await page.goto(`http://127.0.0.1:${parent.address().port}/parent`);
  const frame=page.frames().find(f=>f.url()===`${childOrigin}/frame`);
  await frame.waitForFunction(()=>!!window.fixture);
  const capture=async(html,hidden)=>{
    await page.locator('iframe').evaluate((el,hidden)=>el.style.display=hidden?'none':'block',hidden);
    return frame.evaluate(async({html,hidden})=>{
      const start=performance.now(),png=await window.fixture.capture(html),img=new Image();img.src=png;await img.decode();
      const canvas=document.createElement('canvas');canvas.width=img.width;canvas.height=img.height;canvas.getContext('2d').drawImage(img,0,0);
      const data=canvas.getContext('2d').getImageData(0,0,img.width,img.height).data;
      let changed=0,colored=0;
      for(let i=0;i<data.length;i+=4){if(Math.max(data[i],data[i+1],data[i+2])-Math.min(data[i],data[i+1],data[i+2])>40)colored++;if(hidden&&Math.abs(data[i]-window.baseline[i])+Math.abs(data[i+1]-window.baseline[i+1])+Math.abs(data[i+2]-window.baseline[i+2])>30)changed++;}
      if(!hidden)window.baseline=data;
      return {ms:Math.round(performance.now()-start),width:img.width,height:img.height,colored,difference:changed/(data.length/4)};
    },{html,hidden});
  };
  for(const [name,source] of [['image-svg',imageHTML],['echarts-svg',chartHTML('svg')],['echarts-canvas',chartHTML('canvas')]]){
    const visible=await capture(source,false),hidden=await capture(source,true);
    assert.equal(hidden.width,3200);assert.equal(hidden.height,1800);assert.ok(hidden.colored>10000,`${name}: blank capture`);
    assert.ok(hidden.difference<0.01,`${name}: hidden capture differs: ${hidden.difference}`);
    assert.ok(hidden.ms<15000,`${name}: capture stalled`);
    console.log(name,JSON.stringify({visible,hidden}));
  }
  const slides=process.env.SLIDE_FIXTURE_JSON ? JSON.parse(await readFile(process.env.SLIDE_FIXTURE_JSON,'utf8')) : Array.from({length:9},(_,i)=>slot(imageHTML.replace('Image and SVG',`Slide ${i+1}`),i));
  await frame.evaluate(slots=>window.fixture.show(slots,'hidden-nine'),slides);
  await frame.locator('.slide-thumb__img').nth(8).waitFor({state:'attached',timeout:30000});
  assert.equal(await frame.locator('.slide-thumb__img').count(),9);console.log('hidden rail: 9/9 thumbnails');
  await frame.evaluate(s=>window.fixture.show([s],'recover'),slot(html('<img src="/static-files/retry.svg" width="200" height="200">')));
  await frame.locator('[title="chat.workflowThumbnailFailed"]').waitFor({state:'attached'});
  await page.locator('iframe').evaluate(el=>el.style.display='block');
  await frame.locator('.slide-thumb__img').waitFor({state:'attached',timeout:15000});
  assert.equal(retryRequests,2);console.log('visible recovery: success after one failed asset request');
} finally {await browser?.close();await Promise.all([parent,child].map(server=>new Promise(r=>server.close(r))));}
