import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { echartsOptionToPptx, nativeChartFallbackReasons } from '../lib/echarts_to_pptx.mjs';
import { buildPptx } from '../lib/pptx_builder.mjs';

const types = { bar: 'bar', pie: 'pie', doughnut: 'doughnut' };
const bar = {
  xAxis: [{ type: 'category', data: ['64K'], axisLabel: { color: '#9DB0CC', fontSize: 12 } }],
  yAxis: [{ type: 'value', min: 0, max: 100, interval: 25 }],
  series: [{ type: 'bar', name: 'ETAP', data: [89] }],
};

test('ordinary charts remain native and preserve normalized legend and scale settings', () => {
  const mapped = echartsOptionToPptx({ ...bar, legend: [{ left: 'center', top: 0, textStyle: { fontSize: 12 } }] }, types);
  assert.equal(mapped.chartType, 'bar');
  assert.deepEqual(mapped.data[0].values, [89]);
  assert.equal(mapped.options.legendPos, 't');
  assert.equal(mapped.options.catAxisLabelFontSize, 6.75);
  assert.equal(mapped.options.legendFontSize, 6.75);
  assert.equal(mapped.options.valAxisMinVal, 0);
  assert.equal(mapped.options.valAxisMaxVal, 100);
  assert.equal(mapped.options.valAxisMajorUnit, 25);
  assert.equal(echartsOptionToPptx({ ...bar, legend: [{ show: false }] }, types).options.showLegend, false);
});

test('horizontal charts map category/value axis colors in the correct direction', () => {
  const mapped = echartsOptionToPptx({
    xAxis: [{ type: 'value', axisLabel: { color: '#112233' }, splitLine: { show: false }, min: 0, max: 2 }],
    yAxis: [{ type: 'category', data: ['A'], axisLabel: { color: '#DDEEFF' } }],
    series: [{ type: 'bar', data: [1.25] }],
  }, types);
  assert.equal(mapped.options.catAxisLabelColor, 'DDEEFF');
  assert.equal(mapped.options.valAxisLabelColor, '112233');
  assert.deepEqual(mapped.options.valGridLine, { style: 'none' });
  assert.equal(mapped.options.valAxisMaxVal, 2);
});

test('logarithmic charts keep their rendered visual instead of becoming linear', () => {
  const option = { ...bar, yAxis: [{ type: 'log', min: 1e-5, max: 2.5e-4 }] };
  assert.deepEqual(nativeChartFallbackReasons(option), ['log-axis']);
  assert.equal(echartsOptionToPptx(option, types), null);
});

test('center labels, custom graphic annotations and mark lines are preserved visually', () => {
  for (const graphic of [{ type: 'text', style: { text: '30%' } }, [{ elements: [{ type: 'text', style: { text: '2.78×' } }] }]]) {
    const option = { ...bar, graphic };
    assert.ok(nativeChartFallbackReasons(option).includes('custom-graphics'));
    assert.equal(echartsOptionToPptx(option, types), null);
  }
  assert.equal(echartsOptionToPptx({ ...bar, series: [{ ...bar.series[0], markLine: { data: [{ yAxis: 32 }] } }] }, types), null);
});

test('editable export embeds the rendered chart including labels when native mapping is unsafe', async () => {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'ppt-chart-fidelity-'));
  try {
    const svg = '<svg xmlns="http://www.w3.org/2000/svg" width="300" height="120"><text x="10" y="30" fill="#E6EDF7">10⁻⁵ · RMSE · 15.2×</text><rect x="10" y="50" width="220" height="20" fill="#22D3EE"/></svg>';
    const ir = {
      canvasWidth: 1280, canvasHeight: 720, bodyBgColor: '#070D18', rest: [],
      ct: { tag: 'DIV', styles: {}, bounds: { x: 0, y: 0, w: 1280, h: 720 }, children: [
        { tag: 'DIV', id: 'chart_1', styles: {}, bounds: { x: 100, y: 100, w: 300, h: 120 }, children: [] },
      ] },
      _chartOptions: { chart_1: {
        option: { ...bar, yAxis: [{ type: 'log' }] },
        renderedImageDataUri: 'data:image/svg+xml;charset=UTF-8,' + encodeURIComponent(svg),
      } },
    };
    const output = path.join(dir, 'chart.pptx');
    const result = await buildPptx([{ path: path.join(dir, 'page.html'), ir }], dir, output);
    assert.equal(result.successCount, 1);
    const bytes = await readFile(output);
    assert.notEqual(bytes.indexOf(Buffer.from('10⁻⁵ · RMSE · 15.2×')), -1);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
