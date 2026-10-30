import { expect, it } from 'vitest';
import { layoutPaperRelations, RELATION_NODE_HEIGHT, RELATION_NODE_WIDTH } from './paperRelationLayout';
import type { PaperGraphData } from './paperGraphData';

it('packs connected groups and a large hub without overlapping paper cards or changing relationships', () => {
  const ids = ['baseline', ...Array.from({ length: 11 }, (_, index) => `method-${index}`), 'extension', 'a', 'b'];
  const graph: PaperGraphData = {
    nodes: ids.map(id => ({ id, label: id, tone: 'blue' })),
    edges: [...ids.slice(1, 12).map((id, index) => ({ id: `e-${index}`, source: 'baseline', target: id,
      directed: true, dashed: false, label: 'Method change' })),
    { id: 'extension', source: 'method-3', target: 'extension', directed: true, dashed: false, label: 'Extension' },
    { id: 'comparison', source: 'a', target: 'b', directed: false, dashed: true, label: 'Comparison' }],
  };
  const positions = layoutPaperRelations(graph);
  expect(Object.keys(positions).sort()).toEqual([...ids].sort());
  for (let i = 0; i < ids.length; i++) for (let j = i + 1; j < ids.length; j++) {
    const a = positions[ids[i]], b = positions[ids[j]];
    expect(Math.abs(a.x - b.x) >= RELATION_NODE_WIDTH || Math.abs(a.y - b.y) >= RELATION_NODE_HEIGHT).toBe(true);
  }
  expect(graph.edges.find(edge => edge.id === 'comparison')).toMatchObject({ directed: false, source: 'a', target: 'b' });
});
