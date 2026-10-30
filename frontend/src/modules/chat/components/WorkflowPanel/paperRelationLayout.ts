import type { PaperGraphData } from './paperGraphData';

export const RELATION_NODE_WIDTH = 184;
export const RELATION_NODE_HEIGHT = 64;

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
    const root = ids.reduce((best, id) => neighbors.get(id)!.size > neighbors.get(best)!.size ? id : best);
    const children = new Map(ids.map(id => [id, [] as string[]]));
    const seen = new Set([root]);
    const queue = [root];
    for (let index = 0; index < queue.length; index++) for (const next of neighbors.get(queue[index])!) {
      if (seen.has(next)) continue;
      seen.add(next); children.get(queue[index])!.push(next); queue.push(next);
    }
    const ordered: string[] = [];
    const visit = (id: string) => { for (const child of children.get(id)!) { ordered.push(child); visit(child); } };
    visit(root);
    const points = new Map<string, { x: number; y: number }>([[root, { x: 0, y: 0 }]]);
    if (ordered.length <= 2) {
      ordered.forEach((id, index) => points.set(id, { x: ordered.length === 1 ? 240 : (index ? 240 : -240), y: 0 }));
    } else {
      let columns = 3, rows = 1, best = Infinity;
      for (let count = 3; count <= Math.max(3, Math.ceil(ordered.length / 2)); count++) {
        const sides = Math.max(1, Math.ceil((ordered.length - count * 2) / 2));
        const width = count * RELATION_NODE_WIDTH + (count - 1) * 40;
        const height = (sides + 1) * 100 + RELATION_NODE_HEIGHT;
        const score = Math.abs(Math.log(width / height / 2.5));
        if (score < best) { columns = count; rows = sides; best = score; }
      }
      const step = RELATION_NODE_WIDTH + 40, right = (columns - 1) * step, bottom = (rows + 1) * 100;
      points.set(root, { x: right / 2, y: bottom / 2 });
      const rim: { x: number; y: number }[] = [];
      for (let index = 0; index < columns; index++) rim.push({ x: index * step, y: 0 });
      for (let index = 1; index <= rows; index++) rim.push({ x: right, y: index * 100 });
      for (let index = columns - 1; index >= 0; index--) rim.push({ x: index * step, y: bottom });
      for (let index = rows; index >= 1; index--) rim.push({ x: 0, y: index * 100 });
      ordered.forEach((id, index) => points.set(id, rim[index]));
    }
    const xs = [...points.values()].map(point => point.x), ys = [...points.values()].map(point => point.y);
    const left = Math.min(...xs), top = Math.min(...ys);
    const width = Math.max(...xs) - left + RELATION_NODE_WIDTH;
    const height = Math.max(...ys) - top + RELATION_NODE_HEIGHT;
    if (cursorX > 24 && cursorX + width > 1200) { cursorX = 24; cursorY += rowHeight + 100; rowHeight = 0; }
    points.forEach((point, id) => { positions[id] = { x: point.x - left + cursorX, y: point.y - top + cursorY }; });
    cursorX += width + 100; rowHeight = Math.max(rowHeight, height);
  }
  return positions;
}
