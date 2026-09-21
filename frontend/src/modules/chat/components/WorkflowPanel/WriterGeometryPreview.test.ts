import { expect, it, vi } from 'vitest';
import { createElement } from 'react';
import { render, screen } from '@testing-library/react';
import { registerMap, init } from 'echarts';
vi.mock('echarts',()=>({registerMap:vi.fn(),init:vi.fn()}));
vi.mock('react-i18next',()=>({useTranslation:()=>({t:(key:string)=>key})}));
import { stlVertices, writerGeoJSON, WriterMapPreview } from './WriterGeometryPreview';

it('reads supplied TopoJSON geometry entirely on the frontend', () => {
 const geo = writerGeoJSON(JSON.stringify({type:'Topology',transform:{scale:[2,2],translate:[1,1]},arcs:[[[0,0],[1,0],[0,1],[-1,-1]]],objects:{region:{type:'Polygon',arcs:[[0]]}}}),'topojson');
 expect(geo.features[0].geometry.coordinates[0]).toEqual([[1,1],[3,1],[3,3],[1,1]]);
});
it('accepts textual STL triangles and rejects invalid or unbounded geometry', () => {
 expect(stlVertices('solid x\nvertex 0 0 0\nvertex 1 0 0\nvertex 0 1 0\nendsolid')).toHaveLength(3);
 expect(()=>stlVertices('vertex NaN 0 1')).toThrow();
 expect(()=>stlVertices(' '.repeat(2_000_001))).toThrow();
});

it('bounds expanded polygon coordinates before allocating the repeated arcs',()=>{
 const arc=Array.from({length:1000},(_,i)=>[i%2,Math.floor(i/2)%2]);arc.push([0,0]);
 const source=JSON.stringify({type:'Topology',arcs:[arc],objects:{region:{type:'Polygon',arcs:[Array(1000).fill(0)]}}});
 expect(()=>writerGeoJSON(source,'topojson')).toThrow(/limit|budget/);
});
it('rejects unsupported point and mixed collections instead of rendering empty maps',()=>{
 const point={type:'Feature',properties:{},geometry:{type:'Point',coordinates:[1,2]}};
 const polygon={type:'Feature',properties:{},geometry:{type:'Polygon',coordinates:[[[0,0],[1,0],[0,1],[0,0]]]}};
 for(const features of [[point],[polygon,point]]) expect(()=>writerGeoJSON(JSON.stringify({type:'FeatureCollection',features}),'geojson')).toThrow(/geometry/);
});
it('bounds recursive geometry collections',()=>{
 let geometry:unknown={type:'Polygon',coordinates:[[[0,0],[1,0],[0,1],[0,0]]]};
 for(let i=0;i<40;i++)geometry={type:'GeometryCollection',geometries:[geometry]};
 expect(()=>writerGeoJSON(JSON.stringify({type:'Feature',geometry}),'geojson')).toThrow(/depth|limit/);
});

it('shows an explicit unsupported status without invoking the map renderer',async()=>{
 render(createElement(WriterMapPreview,{language:'geojson',source:JSON.stringify({type:'Feature',geometry:{type:'Point',coordinates:[1,2]}})}));
 expect(await screen.findByRole('status')).toHaveTextContent('chat.writerSource.mapPreviewFailed');
 expect(registerMap).not.toHaveBeenCalled();
 expect(init).not.toHaveBeenCalled();
});
it('rejects malformed or zero-area polygon rings rather than a blank success',()=>{
 for(const ring of [[[0,0],[1,1]],[[0,0],[1,1],[2,2],[0,0]]]) {
  expect(()=>writerGeoJSON(JSON.stringify({type:'Feature',geometry:{type:'Polygon',coordinates:[ring]}}),'geojson')).toThrow(/geometry/);
 }
});
