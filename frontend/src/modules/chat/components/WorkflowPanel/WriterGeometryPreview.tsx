import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

type Position = number[];
interface Geometry { type: string; arcs?: unknown; coordinates?: unknown; geometries?: Geometry[]; properties?: Record<string, unknown> }
interface Topology { type: string; objects: Record<string, Geometry>; arcs: Position[][]; transform?: { scale: Position; translate: Position } }

const MAX_MAP_COORDINATES = 50_000;
const MAX_MAP_DEPTH = 32;

function coordinateBudget() {
  let remaining = MAX_MAP_COORDINATES;
  return (count: number) => {
    remaining -= count;
    if (remaining < 0) throw new Error('Map coordinate limit exceeded');
  };
}

function validateMapGeometry(input: unknown) {
  const consume = coordinateBudget();
  let polygons = 0;
  const coordinates = (value: unknown, depth: number) => {
    if (depth > MAX_MAP_DEPTH) throw new Error('Map depth limit exceeded');
    if (!Array.isArray(value) || !value.length) throw new Error('Invalid geometry coordinates');
    if (typeof value[0] === 'number') {
      if (value.length < 2 || value.length > 3 || !value.every((item) => typeof item === 'number' && Number.isFinite(item))) throw new Error('Invalid geometry position');
      consume(1);
    } else value.forEach((item) => coordinates(item, depth + 1));
  };
  const visit = (value: unknown, depth: number) => {
    if (depth > MAX_MAP_DEPTH) throw new Error('Map depth limit exceeded');
    if (!value || typeof value !== 'object') throw new Error('Invalid geometry');
    const geometry = value as {type?:string;features?:unknown;geometry?:unknown;geometries?:unknown;coordinates?:unknown};
    if (geometry.type === 'FeatureCollection' || geometry.type === 'GeometryCollection') {
      const items = geometry.type === 'FeatureCollection' ? geometry.features : geometry.geometries;
      if (!Array.isArray(items)) throw new Error('Invalid geometry collection');
      items.forEach((item) => visit(item, depth + 1));
    } else if (geometry.type === 'Feature') visit(geometry.geometry, depth + 1);
    else if (geometry.type === 'Polygon' || geometry.type === 'MultiPolygon') {
      coordinates(geometry.coordinates, depth + 1);
      const areas = geometry.type === 'Polygon' ? [geometry.coordinates] : geometry.coordinates as unknown[];
      for (const area of areas) {
        if (!Array.isArray(area) || !area.length) throw new Error('Invalid polygon geometry');
        for (const ring of area) {
          if (!Array.isArray(ring) || ring.length < 4) throw new Error('Invalid polygon geometry');
          const first = ring[0] as number[], last = ring[ring.length - 1] as number[];
          if (!Array.isArray(first) || !Array.isArray(last) || first.length !== last.length || !first.every((v,i) => v === last[i])) throw new Error('Invalid polygon geometry');
          let size = 0;
          for (let i=1; i<ring.length; i++) {
            const previous = ring[i-1] as number[], current = ring[i] as number[];
            size += (previous[0]-first[0])*(current[1]-first[1]) - (current[0]-first[0])*(previous[1]-first[1]);
          }
          if (!Number.isFinite(size) || size === 0) throw new Error('Invalid zero-area geometry');
        }
      }
      polygons++;
    } else throw new Error('Unsupported geometry: area polygons are required');
  };
  visit(input, 0);
  if (!polygons) throw new Error('No supported geometry to preview');
}

// Display-only: unsupported collections fail as a whole, never silently omit data.
export function writerGeoJSON(source: string, language: string) {
  if (source.length > 2_000_000) throw new Error('Map exceeds preview limit');
  const input = JSON.parse(source);
  if (language !== 'topojson') return mapFeatureCollection(input);
  const topology = input as Topology;
  if (topology.type !== 'Topology' || !topology.objects || !Array.isArray(topology.arcs)) throw new Error('Invalid Topology');
  const decodeBudget = coordinateBudget(), expansionBudget = coordinateBudget();
  const point = (p: Position) => {
    if (!Array.isArray(p) || p.length < 2 || p.length > 3 || !p.every(Number.isFinite)) throw new Error('Invalid geometry position');
    return topology.transform ? p.map((v, i) => i < 2 ? v * topology.transform!.scale[i] + topology.transform!.translate[i] : v) : p;
  };
  const arcs = topology.arcs.map((arc) => {
    if (!Array.isArray(arc)) throw new Error('Invalid arc');
    decodeBudget(arc.length);
    let x = 0, y = 0;
    return arc.map((p) => topology.transform ? point([x += p[0], y += p[1]]) : point(p));
  });
  const join = (indexes: number[]) => {
    if (!Array.isArray(indexes)) throw new Error('Invalid arcs');
    return indexes.flatMap((id, index) => {
      if (!Number.isInteger(id)) throw new Error('Invalid arc index');
      const arc = arcs[id < 0 ? ~id : id];
      if (!arc) throw new Error('Invalid arc');
      // Check before reverse, slice or flatMap can allocate the expanded output.
      expansionBudget(Math.max(0, arc.length - (index ? 1 : 0)));
      const points = id < 0 ? [...arc].reverse() : arc;
      return index ? points.slice(1) : points;
    });
  };
  const geometry = (item: Geometry, depth = 0): unknown => {
    if (depth > MAX_MAP_DEPTH) throw new Error('Map depth limit exceeded');
    if (!item || typeof item !== 'object') throw new Error('Invalid geometry');
    switch (item.type) {
      case 'GeometryCollection': return { type:item.type, geometries:item.geometries?.map((child) => geometry(child, depth + 1)) };
      case 'Polygon': return { type:item.type, coordinates:(item.arcs as number[][]).map(join) };
      case 'MultiPolygon': return { type:item.type, coordinates:(item.arcs as number[][][]).map((polygon) => polygon.map(join)) };
      default: throw new Error('Unsupported geometry: area polygons are required');
    }
  };
  const output = {type:'FeatureCollection',features:Object.values(topology.objects).map((item) => ({type:'Feature',properties:item.properties ?? {},geometry:geometry(item)}))};
  return mapFeatureCollection(output);
}

function mapFeatureCollection(input: unknown) {
  validateMapGeometry(input);
  type MapNode = Geometry & { features?: MapNode[]; geometry?: MapNode; geometries?: MapNode[] };
  type Area = {type:'Polygon';coordinates:number[][][]} | {type:'MultiPolygon';coordinates:number[][][][]};
  const features: {type:'Feature';properties:Record<string,unknown> & {name:string};geometry:Area}[] = [];
  const visit = (node: MapNode, properties: Record<string,unknown> = {}) => {
    if (node.type === 'FeatureCollection') node.features!.forEach((child) => visit(child));
    else if (node.type === 'Feature') visit(node.geometry!, node.properties && typeof node.properties === 'object' ? node.properties : {});
    else if (node.type === 'GeometryCollection') node.geometries!.forEach((child) => visit(child, properties));
    else features.push({type:'Feature', properties:{...properties,name:typeof properties.name === 'string' ? properties.name : String(features.length + 1)},geometry:node as Area});
  };
  visit(input as MapNode);
  return {type:'FeatureCollection' as const,features};
}

export function WriterMapPreview({ source, language }: { source: string; language: string }) {
  const { t } = useTranslation();
  const host = useRef<HTMLDivElement>(null);
  const mapId = useRef(`writer-map-${crypto.randomUUID()}`);
  const controls = useRef<{ zoom: (factor: number) => void; reset: () => void }>();
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let canceled = false;
    let dispose: (() => void) | undefined;
    setFailed(false);
    void import('echarts').then((echarts) => {
      if (canceled || !host.current) return;
      echarts.registerMap(mapId.current, writerGeoJSON(source, language));
      const chart = echarts.init(host.current);
      dispose = () => chart.dispose();
      chart.setOption({ series: [{ type: 'map', map: mapId.current, roam: true, emphasis: { label: { show: true } } }] });
      controls.current = {
        zoom: (zoom) => chart.dispatchAction({ type: 'geoRoam', seriesIndex: 0, zoom }),
        reset: () => chart.setOption({ series: [{ zoom: 1, center: null }] }),
      };
      const observer = new ResizeObserver(() => chart.resize());
      observer.observe(host.current);
      dispose = () => { controls.current = undefined; observer.disconnect(); chart.dispose(); };
    }).catch(() => { if (!canceled) { dispose?.(); setFailed(true); } });
    return () => { canceled = true; dispose?.(); };
  }, [source, language]);
  return <div>
    {failed && <p role='status'>{t('chat.writerSource.mapPreviewFailed')}</p>}
    <div ref={host} className='writer-source__geometry' role='img' aria-label={t('chat.writerSource.map')} />
    <button type='button' disabled={failed} onClick={() => controls.current?.zoom(1.25)}>{t('chat.writerSource.zoomIn')}</button>
    <button type='button' disabled={failed} onClick={() => controls.current?.zoom(0.8)}>{t('chat.writerSource.zoomOut')}</button>
    <button type='button' disabled={failed} onClick={() => controls.current?.reset()}>{t('chat.writerSource.reset')}</button>
  </div>;
}

export function stlVertices(source: string): number[][] {
  if (source.length > 2_000_000) throw new Error('Model exceeds preview limit');
  const vertices = [...source.matchAll(/\bvertex\s+([-+\d.eE]+)\s+([-+\d.eE]+)\s+([-+\d.eE]+)/g)].map((m) => m.slice(1).map(Number));
  if (!vertices.length || vertices.length % 3 || vertices.length > 60000 || vertices.some((v) => v.some((n) => !Number.isFinite(n)))) throw new Error('Invalid STL');
  return vertices;
}

export function WriterModelPreview({ source }: { source: string }) {
  const { t } = useTranslation();
  const canvas = useRef<HTMLCanvasElement>(null);
  const [rotation, setRotation] = useState(30);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    try {
      const vertices = stlVertices(source);
      const context = canvas.current?.getContext('2d');
      if (!context) return;
      const min = [0, 1, 2].map((axis) => Math.min(...vertices.map((v) => v[axis])));
      const max = [0, 1, 2].map((axis) => Math.max(...vertices.map((v) => v[axis])));
      const extent = Math.max(...max.map((v, i) => v - min[i]), 0.001);
      const angle = rotation * Math.PI / 180;
      const points = vertices.map((v) => {
        const [x, y, z] = v.map((n, i) => (n - (min[i] + max[i]) / 2) / extent * 250);
        return [260 + x * Math.cos(angle) - z * Math.sin(angle), 160 - y * 0.9 - (x * Math.sin(angle) + z * Math.cos(angle)) * 0.3];
      });
      context.clearRect(0, 0, 520, 320);
      context.strokeStyle = '#2563eb'; context.lineWidth = 0.7;
      for (let i = 0; i < points.length; i += 3) {
        context.beginPath(); context.moveTo(points[i][0], points[i][1]);
        for (let j = 1; j < 3; j++) context.lineTo(points[i + j][0], points[i + j][1]);
        context.closePath(); context.stroke();
      }
      setFailed(false);
    } catch { setFailed(true); }
  }, [source, rotation]);
  return <div>
    {failed && <p role='status'>{t('chat.writerSource.previewFailed')}</p>}
    <canvas ref={canvas} hidden={failed} width={520} height={320} className='writer-source__model' aria-label={t('chat.writerSource.model')} />
    <label>{t('chat.writerSource.rotate')}<input type='range' disabled={failed} min={0} max={360} value={rotation} onChange={(e) => setRotation(Number(e.target.value))} /></label>
  </div>;
}
