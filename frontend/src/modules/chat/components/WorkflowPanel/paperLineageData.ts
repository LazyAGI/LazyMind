import type { PaperRelation, WorkflowPaper } from '@/modules/knowledge/api/paperWorkflows';
import type { PaperGraphData } from './paperGraphData';

export const paperNodeId = (paperId: string) => `p_${paperId}`;
export const isDirectedPaperRelation = (type: PaperRelation['relation_type']) =>
  ['method_modification', 'design_inspiration', 'component_use', 'background_citation'].includes(type);
export function paperRelationshipGraph(papers: WorkflowPaper[], relations: PaperRelation[]): PaperGraphData {
  const known = new Set(papers.map(item => item.paper.paper_id));
  const edges = relations.filter(item => known.has(item.source_paper_id) && known.has(item.target_paper_id)).map(item => {
    const directed = isDirectedPaperRelation(item.relation_type);
    return { id: item.id, source: paperNodeId(item.source_paper_id), target: paperNodeId(item.target_paper_id),
      label: item.relation_type, relationType: item.relation_type, directed, dashed: false };
  });
  const connected = new Set(edges.flatMap(item => [item.source, item.target]));
  return { nodes: papers.map(item => ({ id: paperNodeId(item.paper.paper_id), label: paperGraphTitle(item.paper.title || item.paper.paper_id), tone: item.is_seed ? 'amber' : 'blue' }))
    .filter(node => connected.has(node.id)), edges };
}

/** A named method before a subtitle is a source-provided short title. */
export function paperGraphTitle(title: string): string {
  const explicitAcronym = title.match(/\(([A-Z][A-Z0-9-]{1,14})\)/)?.[1];
  if (explicitAcronym) return explicitAcronym;
  const separator = title.search(/[:：]/);
  return separator > 0 && separator <= 42 ? title.slice(0, separator).trim() : title;
}
