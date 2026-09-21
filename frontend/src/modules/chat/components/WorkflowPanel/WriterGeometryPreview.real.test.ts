import { expect, it } from 'vitest';
import * as echarts from 'echarts';
import { writerGeoJSON } from './WriterGeometryPreview';

it('renders accepted polygon wrappers and null properties with real ECharts', () => {
  const polygon = { type: 'Polygon', coordinates: [[[0,0],[1,0],[0,1],[0,0]]] };
  const inputs = [polygon, { type: 'Feature', properties: { name: 'A' }, geometry: polygon },
    { type: 'FeatureCollection', features: [{ type: 'Feature', properties: null, geometry: polygon }] },
    { type: 'GeometryCollection', geometries: [polygon] },
    { type: 'FeatureCollection', features: [{ type: 'Feature', properties: { name: 'A' }, geometry: polygon }] }];
  inputs.forEach((input, index) => {
    const name = `writer-test-${index}`;
    echarts.registerMap(name, writerGeoJSON(JSON.stringify(input), 'geojson'));
    const chart = echarts.init(null, null, { renderer: 'svg', ssr: true, width: 520, height: 320 });
    try {
      chart.setOption({ series: [{ type: 'map', map: name }] });
      expect(chart.renderToSVGString()).toContain('<path');
    } finally { chart.dispose(); }
  });
});
