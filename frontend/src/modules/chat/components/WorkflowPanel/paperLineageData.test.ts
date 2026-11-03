import { expect, it } from 'vitest';
import type { PaperRelation, WorkflowPaper } from '@/modules/knowledge/api/paperWorkflows';
import { paperRelationshipGraph, verifiedPaperRelations } from './paperLineageData';
const papers: WorkflowPaper[] = Array.from({ length: 4 }, (_, index) => ({ paper: { paper_id: `0000000${index}-1111-2222-3333-444444444444`, title: `Paper ${index}`, abstract: null }, is_seed: index === 0, is_important: false }));
const ids = papers.map(item => item.paper.paper_id);
it('preserves candidate relationships with distinct verification statuses', () => {
  const relation = (id: string, status: NonNullable<PaperRelation['verification']>['status'], scope_status: NonNullable<PaperRelation['verification']>['scope_status']): PaperRelation => ({ id, source_paper_id: ids[1], target_paper_id: ids[2], relation_type: 'experimental_comparison', conditions: '', statement: 'Same evaluation setting', evidence: [{ paper_id: ids[1], positions: [2] }], verification: { status, scope_status } });
  const relations: PaperRelation[] = [relation('verified', 'supported', 'supported'), relation('unscoped', 'supported', 'uncertain'), relation('contradicted', 'unsupported', 'supported'), { ...relation('error', 'supported', 'supported'), verification: { status: 'supported', scope_status: 'supported', verification_error: 'stage_timeout' } }];
  expect(verifiedPaperRelations(relations).map(item => item.id)).toEqual(['verified']);
  expect(paperRelationshipGraph(papers, relations).edges.map(item => [item.id, item.status])).toEqual([['verified', 'verified'], ['unscoped', 'pending'], ['contradicted', 'unsupported'], ['error', 'timeout']]);
});
it('marks only method modifications as inheritance-style arrows and uses short type labels', () => {
  const types: PaperRelation['relation_type'][] = ['method_modification', 'design_inspiration', 'component_use', 'experimental_comparison', 'mechanism_comparison', 'background_citation'];
  const relations: PaperRelation[] = types.map(type => ({ id: type, source_paper_id: ids[1], target_paper_id: ids[2], relation_type: type, conditions: '', statement: 'A long statement that belongs in evidence details', evidence: [], verification: { status: 'supported', scope_status: 'supported' } }));
  const graph = paperRelationshipGraph(papers, relations);
  expect(graph.edges.find(edge => edge.id === 'method_modification')).toMatchObject({ directed: true, dashed: false, label: 'method_modification' });
  expect(graph.edges.filter(edge => edge.id !== 'method_modification').every(edge => !edge.directed && !edge.dashed)).toBe(true);
  expect(graph.edges.map(edge => edge.label)).toEqual(types);

});

it('keeps timed-out method candidates visible without inheritance arrows', () => {
  const relations: PaperRelation[] = [{ id: 'candidate', source_paper_id: ids[1], target_paper_id: ids[2], relation_type: 'method_modification', conditions: '', statement: 'Candidate only', evidence: [], verification: { status: 'uncertain', verification_error: 'stage_timeout' } }];
  const graph = paperRelationshipGraph(papers, relations);
  expect(graph.edges).toEqual([expect.objectContaining({ id: 'candidate', status: 'timeout', directed: false, dashed: true })]);
  expect(graph.nodes).toHaveLength(2);
  expect(verifiedPaperRelations(relations)).toHaveLength(0);
});
