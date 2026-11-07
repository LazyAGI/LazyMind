import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { chromium } from 'playwright';

const require = createRequire(import.meta.url);
test('script palette edit keeps layout, evidence, images and chart data while fixing SVG/chart text', async () => {
  const browser = await chromium.launch({headless: true});
  try {
    const page = await browser.newPage();
    await page.setContent(`<style>.wrapper{width:1280px;height:720px;background:#070D18;color:#E6EDF7}.panel{background:rgba(19,32,53,.72);width:500px;height:100px}svg text{fill:#E6EDF7}</style><div class="wrapper"><div class="panel"><h1>结论89 TFLOPS</h1><img src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"></div><svg width="300" height="50"><text x="10" y="30">M=16</text></svg><div id="chart_1" style="width:500px;height:300px"></div><div id="chart_2"><svg><circle fill="#334155"/><text fill="#E6EDF7">30%</text></svg></div></div>`);
    await page.addScriptTag({path: require.resolve('echarts/dist/echarts.min.js')});
    await page.evaluate(() => {
      const chart = echarts.init(document.querySelector('#chart_1'), null, {renderer:'svg'});
      chart.setOption({animation:false,xAxis:{type:'category',data:['64K'],axisLabel:{color:'#E6EDF7'}},yAxis:{type:'value'},series:[{type:'bar',data:[89],itemStyle:{color:'#22D3EE'}}]});
    });
    const inspect = () => page.evaluate(() => {
      const el=document.querySelector('.panel'),r=el.getBoundingClientRect(),o=echarts.getInstanceByDom(document.querySelector('#chart_1')).getOption();
      return {text:el.textContent,image:el.querySelector('img').src,box:[r.x,r.y,r.width,r.height],bg:getComputedStyle(document.querySelector('.wrapper')).backgroundColor,color:getComputedStyle(el.querySelector('h1')).color,svg:getComputedStyle(document.querySelector('svg text')).fill,fallback:getComputedStyle(document.querySelector('#chart_2 text')).fill,data:o.series[0].data,seriesColor:o.series[0].itemStyle.color,axisColor:o.xAxis[0].axisLabel.color};
    });
    const before = await inspect();
    const script = await readFile(new URL('../../recolor_slide.js', import.meta.url), 'utf8');
    for (const theme of ['light','dark']) {
      await page.evaluate(({script,theme}) => {const el=document.createElement('script');el.dataset.theme=theme;el.textContent=script;document.body.append(el);}, {script,theme});
      const after=await inspect();
      for (const key of ['text','image','box','data','seriesColor']) assert.deepEqual(after[key],before[key],key);
      assert.equal(after.bg,theme==='light'?'rgb(255, 255, 255)':'rgb(11, 18, 32)');
      assert.equal(after.color,theme==='light'?'rgb(17, 17, 17)':'rgb(241, 245, 249)');
      assert.equal(after.svg,after.color);
      assert.equal(after.fallback,after.color);
      assert.equal(after.axisColor,theme==='light'?'#475569':'#B8C5D6');
    }
  } finally { await browser.close(); }
});
