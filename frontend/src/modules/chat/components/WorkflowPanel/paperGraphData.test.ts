import { expect, it } from 'vitest';
import { branchDescendants, connectedPaperGraph, parsePaperGraph, readingPaperGraph, visiblePaperGraph } from './paperGraphData';

it('preserves declared paper identities, edge meaning and branch membership when folded', async () => {
  const graph = await parsePaperGraph(`flowchart LR
q("研究问题") --- route_a("共同基础")
q --- route_b("优化方法")
route_a --- p_a("1 基线")
route_b --- p_b("2 方法B")
route_b --- p_c("3 方法C")
class route_b,p_b,p_c cyan;`);
  const branches = branchDescendants(graph);
  const folded = visiblePaperGraph(graph, new Set(['route_b']), branches);
  expect(folded.nodes.map(node => node.id)).toEqual(['q', 'route_a', 'route_b', 'p_a']);
  expect(folded.edges.every(edge => !['p_b', 'p_c'].includes(edge.target))).toBe(true);
  expect(graph.nodes.find(node => node.id === 'p_b')).toMatchObject({ label: '2 方法B', tone: 'cyan' });
  expect(visiblePaperGraph(graph, new Set(), branches).nodes.filter(node => node.id.startsWith('p_'))).toHaveLength(3);
  expect(graph.edges.every(edge => !edge.directed)).toBe(true);
});

it('keeps method arrows and comparison lines distinct without adding relationships', async () => {
  const graph = await parsePaperGraph(`flowchart LR
p_a("基线") -->|改动初始化| p_b("方法B")
p_b -.-|同一任务对照| p_c("方法C")
p_d("未确认直接关系")`);
  expect(graph.nodes).toHaveLength(4);
  expect(graph.edges).toEqual([
    expect.objectContaining({ source: 'p_a', target: 'p_b', label: '改动初始化', directed: true, dashed: false }),
    expect.objectContaining({ source: 'p_b', target: 'p_c', label: '同一任务对照', directed: false, dashed: true }),
  ]);
  const main = connectedPaperGraph(graph);
  expect(main.nodes.map(node => node.id)).toEqual(['p_a', 'p_b', 'p_c']);
  expect(main.edges).toEqual(graph.edges);
});

it('opens a rooted reading tree one topic level at a time', async () => {
  const graph = await parsePaperGraph(`flowchart LR
q_root("总研究问题") --- route_main("主要主题")
route_main --- route_a("子主题 A")
route_main --- route_b("子主题 B")
route_a --- p_a("1 论文 A")
route_b --- p_b("2 论文 B")`);
  const branches = branchDescendants(graph);
  const folded = new Set(branches.keys());
  expect(visiblePaperGraph(graph, folded, branches).nodes.map(node => node.id)).toEqual(['q_root', 'route_main']);
  folded.delete('route_main');
  expect(visiblePaperGraph(graph, folded, branches).nodes.map(node => node.id))
    .toEqual(['q_root', 'route_main', 'route_a', 'route_b']);
  folded.delete('route_a');
  expect(visiblePaperGraph(graph, folded, branches).nodes.filter(node => node.id.startsWith('p_')).map(node => node.id))
    .toEqual(['p_a']);
});

it('shows a recorded reading chain as peer papers within its original topic', async () => {
  const graph = readingPaperGraph(await parsePaperGraph(`flowchart TD
q("研究问题") --- route_a("训练信号")
q --- route_b("多步编排")
route_a --> p_a("1 Paper A")
p_a --> p_b("2 Paper B")
route_b --> p_c("3 Paper C")`));
  expect(graph.nodes.filter(node => node.id.startsWith('p_')).map(node => node.label)).toEqual(['1 Paper A', '2 Paper B', '3 Paper C']);
  expect(graph.edges.filter(edge => edge.source === 'route_a').map(edge => edge.target)).toEqual(['p_a', 'p_b']);
  expect(graph.edges.filter(edge => edge.source === 'route_b').map(edge => edge.target)).toEqual(['p_c']);
  expect(graph.edges.every(edge => !edge.source.startsWith('p_') && !edge.directed)).toBe(true);
  const folded = visiblePaperGraph(graph, new Set(['route_a']), branchDescendants(graph));
  expect(folded.nodes.filter(node => node.id.startsWith('p_')).map(node => node.id)).toEqual(['p_c']);
});
