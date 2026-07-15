import type { EdgeVisual, GraphModel, NodeLayout } from './model';

export const NODE_MIN_HEIGHT = 64;
export const edgeId = (source: string, target: string) => `${source}->${target}`;

export function serializeLayout(model: Pick<GraphModel, 'layout' | 'edgeLayout'>): string {
  const doc: Record<string, unknown> = {};
  for (const [id, value] of Object.entries(model.layout)) doc[id] = value;
  const edges = Object.fromEntries(Object.entries(model.edgeLayout).filter(([, value]) => Object.keys(value).length));
  if (Object.keys(edges).length) {
    doc.$meta = { version: 2 };
    doc.$edges = edges;
  }
  return JSON.stringify(doc);
}

export function renameLayoutNode(
  layout: Record<string, NodeLayout>, edgeLayout: Record<string, EdgeVisual>, oldId: string, newId: string,
) {
  const nextLayout = { ...layout };
  if (nextLayout[oldId]) {
    nextLayout[newId] = nextLayout[oldId];
    delete nextLayout[oldId];
  }
  const nextEdges: Record<string, EdgeVisual> = {};
  for (const [id, style] of Object.entries(edgeLayout)) {
    const split = id.indexOf('->');
    const source = id.slice(0, split);
    const target = id.slice(split + 2);
    nextEdges[edgeId(source === oldId ? newId : source, target === oldId ? newId : target)] = style;
  }
  return { layout: nextLayout, edgeLayout: nextEdges };
}

export function deleteLayoutNode(
  layout: Record<string, NodeLayout>, edgeLayout: Record<string, EdgeVisual>, nodeId: string,
) {
  const nextLayout = { ...layout };
  delete nextLayout[nodeId];
  return {
    layout: nextLayout,
    edgeLayout: Object.fromEntries(Object.entries(edgeLayout).filter(([id]) => {
      const split = id.indexOf('->');
      return id.slice(0, split) !== nodeId && id.slice(split + 2) !== nodeId;
    })),
  };
}

export function reconnectEdgeLayout(edgeLayout: Record<string, EdgeVisual>, oldId: string, newId: string) {
  const next = { ...edgeLayout };
  if (next[oldId]) next[newId] = next[oldId];
  delete next[oldId];
  return next;
}
