import type { FlowDB } from 'mermaid/dist/diagrams/flowchart/flowDb.js';
import { getMermaid } from '../MarkdownViewer/mermaid';

export type PaperGraphData = {
  nodes: { id: string; label: string; tone: string }[];
  edges: { id: string; source: string; target: string; label: string; directed: boolean; dashed: boolean }[];
};

/** Project the report's declared graph, without inventing paper relationships. */
export async function parsePaperGraph(code: string): Promise<PaperGraphData> {
  const mermaid = await getMermaid();
  await mermaid.parse(code);
  const diagram = await mermaid.mermaidAPI.getDiagramFromText(code);
  if (!diagram.type.startsWith('flowchart')) throw new Error('Expected a flowchart');
  const data = (diagram.db as FlowDB).getData();
  const plain = (label: string) => {
    const node = document.createElement('div');
    node.innerHTML = label;
    return node.textContent || '';
  };
  const nodes = data.nodes.map(node => ({ id: node.id, label: plain(String(node.label ?? node.id)),
    tone: node.cssClasses?.split(/\s+/).find(value => ['blue', 'cyan', 'purple', 'amber', 'rose', 'qnode'].includes(value)) || 'blue' }));
  const edges = data.edges.map((edge, index) => ({ id: `edge-${index}`, source: edge.start!, target: edge.end!,
    label: plain(String(edge.label ?? '')), directed: edge.arrowTypeEnd === 'arrow_point', dashed: edge.pattern === 'dotted' }));
  // A Mermaid subgraph is explicit organizational membership, not method inheritance.
  data.nodes.forEach(node => {
    if (node.parentId) edges.push({ id: `group-${node.id}`, source: node.parentId, target: node.id,
      label: '', directed: false, dashed: false });
  });
  return { nodes, edges };
}

export function branchDescendants(graph: PaperGraphData): Map<string, Set<string>> {
  const children = new Map<string, string[]>();
  graph.edges.forEach(edge => children.set(edge.source, [...children.get(edge.source) || [], edge.target]));
  return new Map(graph.nodes.filter(node => node.id.startsWith('route_')).map(node => {
    const seen = new Set<string>([node.id]);
    const pending = [...children.get(node.id) || []];
    for (let index = 0; index < pending.length; index++) {
      const id = pending[index];
      if (seen.has(id)) continue;
      seen.add(id); pending.push(...children.get(id) || []);
    }
    seen.delete(node.id);
    return [node.id, seen];
  }));
}

/** Reading chains encode order. Keep their declared groups and show papers as peer leaves. */
export function readingPaperGraph(graph: PaperGraphData): PaperGraphData {
  const papers = new Set(graph.nodes.filter(node => node.id.startsWith('p_')).map(node => node.id));
  const children = new Map<string, string[]>();
  graph.edges.forEach(edge => children.set(edge.source, [...children.get(edge.source) || [], edge.target]));
  const edges = graph.edges.filter(edge => !papers.has(edge.source) && !papers.has(edge.target))
    .map(edge => ({ ...edge, directed: false, dashed: false }));
  for (const group of graph.nodes.filter(node => !papers.has(node.id))) {
    const seen = new Set<string>();
    const pending = [...children.get(group.id) || []];
    for (let index = 0; index < pending.length; index++) {
      const id = pending[index];
      if (!papers.has(id) || seen.has(id)) continue;
      seen.add(id);
      edges.push({ id: `reading-${edges.length}`, source: group.id, target: id, label: '', directed: false, dashed: false });
      pending.push(...children.get(id) || []);
    }
  }
  return { nodes: graph.nodes, edges };
}

export function visiblePaperGraph(graph: PaperGraphData, folded: Set<string>, branches: Map<string, Set<string>>) {
  const hidden = new Set([...folded].flatMap(id => [...branches.get(id) || []]));
  return { nodes: graph.nodes.filter(node => !hidden.has(node.id)),
    edges: graph.edges.filter(edge => !hidden.has(edge.source) && !hidden.has(edge.target)) };
}

export function connectedPaperGraph(graph: PaperGraphData): PaperGraphData {
  const connected = new Set(graph.edges.flatMap(edge => [edge.source, edge.target]));
  return { nodes: graph.nodes.filter(node => connected.has(node.id)), edges: graph.edges };
}
