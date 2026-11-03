import type { PaperGraphData } from './paperGraphData';

export const RELATION_NODE_WIDTH = 156;
export const RELATION_NODE_HEIGHT = 60;

/** Place each connected group around its most connected paper; edges remain unchanged. */
export function layoutPaperRelations(graph: PaperGraphData): Record<string, { x: number; y: number }> {
  const neighbors = new Map(graph.nodes.map(node => [node.id, new Set<string>()]));
  graph.edges.forEach(edge => {
    neighbors.get(edge.source)!.add(edge.target);
    neighbors.get(edge.target)!.add(edge.source);
  });
  const remaining = new Set(neighbors.keys());
  const components: string[][] = [];
  while (remaining.size) {
    const ids = [remaining.values().next().value!];
    remaining.delete(ids[0]);
    for (let index = 0; index < ids.length; index++) for (const next of neighbors.get(ids[index])!) {
      if (remaining.delete(next)) ids.push(next);
    }
    components.push(ids);
  }
  components.sort((a, b) => b.length - a.length);
  const positions: Record<string, { x: number; y: number }> = {};
  let cursorX = 24, cursorY = 24, rowHeight = 0;
  for (const ids of components) {
    const root = ids.find(id => graph.nodes.find(node => node.id === id)?.tone === 'amber')
      || ids.reduce((best, id) => neighbors.get(id)!.size > neighbors.get(best)!.size ? id : best);
    const others = ids.filter(id => id !== root);
    const rows = Math.ceil(others.length / 2);
    const points = new Map<string, { x: number; y: number }>();
    const sideX = 500;
    points.set(root, { x: sideX / 2, y: (rows - 1) * 74 / 2 });
    others.forEach((id, index) => points.set(id, {
      x: index % 2 ? sideX : 0,
      y: Math.floor(index / 2) * 74,
    }));
    const xs = [...points.values()].map(point => point.x), ys = [...points.values()].map(point => point.y);
    const left = Math.min(...xs), top = Math.min(...ys);
    const width = Math.max(...xs) - left + RELATION_NODE_WIDTH;
    const height = Math.max(...ys) - top + RELATION_NODE_HEIGHT;
    if (cursorX > 24) { cursorX = 24; cursorY += rowHeight + 100; rowHeight = 0; }
    points.forEach((point, id) => { positions[id] = { x: point.x - left + cursorX, y: point.y - top + cursorY }; });
    cursorX += width + 100; rowHeight = Math.max(rowHeight, height);
  }
  return positions;
}
