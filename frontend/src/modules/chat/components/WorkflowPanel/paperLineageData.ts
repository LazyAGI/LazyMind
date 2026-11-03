import type { PaperRelation, WorkflowPaper } from '@/modules/knowledge/api/paperWorkflows';
import type { PaperGraphData } from './paperGraphData';

export const paperNodeId = (paperId: string) => `p_${paperId}`;
export function paperRelationStatus(relation: PaperRelation): 'verified' | 'pending' | 'timeout' | 'error' | 'unsupported' {
  const verification = relation.verification;
  if (verification.verification_error) return verification.verification_error === 'stage_timeout' ? 'timeout' : 'error';
  if (verification.status === 'supported' && verification.scope_status === 'supported') return 'verified';
  if (verification.status === 'unsupported' || verification.scope_status === 'unsupported') return 'unsupported';
  return 'pending';
}
export const verifiedPaperRelations = (relations: PaperRelation[]) => relations.filter(relation => paperRelationStatus(relation) === 'verified');
export function paperRelationshipGraph(papers: WorkflowPaper[], relations: PaperRelation[]): PaperGraphData {
  const known = new Set(papers.map(item => item.paper.paper_id));
  const edges = relations.filter(item => known.has(item.source_paper_id) && known.has(item.target_paper_id)).map(item => {
    const status = paperRelationStatus(item);
    const directed = status === 'verified' && item.relation_type === 'method_modification';
    return { id: item.id, source: paperNodeId(item.source_paper_id), target: paperNodeId(item.target_paper_id),
      label: item.relation_type, relationType: item.relation_type, status, directed, dashed: status !== 'verified' };
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
  if (/^You Only Cache Once\b/i.test(title)) return 'YOCO';
  if (/^You Only Index Once\b/i.test(title)) return 'YOIO';
  if (/\bCross-Layer Attention\b/i.test(title)) return 'CLA';
  return separator > 0 && separator <= 42 ? title.slice(0, separator).trim() : title;
}
