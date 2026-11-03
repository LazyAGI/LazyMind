import { expect, it } from 'vitest';
import type { PaperRelation, WorkflowPaper } from '@/modules/knowledge/api/paperWorkflows';
import { paperRelationshipGraph } from './paperLineageData';
const papers: WorkflowPaper[] = Array.from({ length: 4 }, (_, index) => ({ paper: { paper_id: `0000000${index}-1111-2222-3333-444444444444`, title: `Paper ${index}`, abstract: null }, is_seed: index === 0, is_important: false }));
const ids = papers.map(item => item.paper.paper_id);
it('uses relationship semantics for direction and retains every known edge', () => {
  const types: PaperRelation['relation_type'][] = ['method_modification', 'design_inspiration', 'component_use', 'experimental_comparison', 'mechanism_comparison', 'background_citation'];
  const relations: PaperRelation[] = types.map(type => ({ id: type, source_paper_id: ids[1], target_paper_id: ids[2], relation_type: type, conditions: '', statement: 'Research analysis', evidence: [] }));
  const graph = paperRelationshipGraph(papers, relations);
  expect(graph.edges.map(edge => edge.label)).toEqual(types);
  expect(graph.edges.filter(edge => edge.directed).map(edge => edge.id)).toEqual(['method_modification', 'design_inspiration', 'component_use', 'background_citation']);
  expect(graph.edges.every(edge => !edge.dashed)).toBe(true);
  expect(graph.nodes).toHaveLength(2);
});

it('does not draw dangling endpoints outside the saved paper set', () => {
  const relation: PaperRelation = { id: 'missing', source_paper_id: ids[0], target_paper_id: 'absent', relation_type: 'component_use', conditions: '', statement: 'Uses component', evidence: [] };
  expect(paperRelationshipGraph(papers, [relation]).edges).toEqual([]);
});
