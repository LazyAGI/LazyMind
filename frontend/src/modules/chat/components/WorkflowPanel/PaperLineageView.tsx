import { useMemo, useRef, useState, type ReactNode } from 'react';
import { Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import type { PaperRelation, PaperResearchStructure, WorkflowPaper } from '@/modules/knowledge/api/paperWorkflows';
import PaperGraph from './PaperGraph';
import { paperNodeId, paperRelationshipGraph, isDirectedPaperRelation } from './paperLineageData';

function EvidencePosition({ evidence }: { evidence: PaperRelation['evidence'][number] }) {
  const { t } = useTranslation();
  const tr = (key: string) => t(`paperTasks.workflowResult.${key}`);
  return <><span title={`${tr('rawBlockPositions')}: ${evidence.positions.join(', ')}`}>
    {tr('evidencePositions')}: {evidence.positions.map(position => position + 1).join(', ') || tr('noEvidencePositions')}</span>
    {(evidence.content_hash || evidence.source_version) && <details className='paper-lineage-source'><summary>{tr('evidenceSourceVersion')}</summary>
      {evidence.source_version && <p>{t('paperTasks.workflowResult.sourceVersion', { version: evidence.source_version })}</p>}
      {evidence.content_hash && <p>{tr('contentHash')}: <code>{evidence.content_hash}</code></p>}
    </details>}
  </>;
}

type Props = {
 papers: WorkflowPaper[]; structure?: PaperResearchStructure; relations: PaperRelation[];
 renderDetail: (paper: WorkflowPaper) => ReactNode;
};
export default function PaperLineageView({ papers, structure, relations, renderDetail }: Props) {
  const { t } = useTranslation();
  const tr = (key: string) => t(`paperTasks.workflowResult.${key}`);
  const unique = useMemo(() => [...new Map(papers.map(item => [item.paper.paper_id, item])).values()], [papers]);
  const byId = useMemo(() => new Map(unique.map(item => [item.paper.paper_id, item])), [unique]);
  const [selected, setSelected] = useState<string>();
  const detail = useRef<HTMLDivElement>(null);
  const evidence = useRef<HTMLDetailsElement>(null);
  const openPaper = (id: string) => { setSelected(id); requestAnimationFrame(() => detail.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })); };
  const links = Object.fromEntries(unique.map(item => [paperNodeId(item.paper.paper_id), { title: item.paper.title || tr('untitled'),
    subtitle: [item.is_seed ? tr('seed') : '', item.paper.year].filter(value => value !== undefined && value !== '').join(' · '),
    label: t('paperTasks.workflowResult.viewPaper', { title: item.paper.title || tr('untitled') }), onClick: () => openPaper(item.paper.paper_id) }]));
  const graph = useMemo(() => {
    const data = paperRelationshipGraph(unique, relations);
    return { ...data, edges: data.edges.map(edge => {
      const relation = relations.find(relation => relation.id === edge.id)!;
      const statement = relation.statement.trim().replace(/\s+/g, ' ');
      return { ...edge,
        label: tr(`relationType_${edge.label}`),
        description: statement,
      };
    }) };
  }, [unique, relations, t]);
  const selectedPaper = selected ? byId.get(selected) : undefined;
  const placement = structure?.nodes.find(item => item.paper_id === selected);
  const direction = structure?.directions.find(item => item.id === placement?.direction_id);
  const associated = relations.filter(item => item.source_paper_id === selected || item.target_paper_id === selected);
  const paperLink = (id: string) => <button type='button' className='paper-workflow-text-button' onClick={() => openPaper(id)}>{byId.get(id)?.paper.title || id}</button>;
  return <div className='paper-lineage'>
    <PaperGraph graph={graph} nodeLinks={links} paperCount={unique.length}
      onViewEvidence={() => { if (evidence.current) { evidence.current.open = true; evidence.current.scrollIntoView({ block: 'nearest', behavior: 'smooth' }); } }} />
    {selectedPaper && <div ref={detail} className='paper-lineage-detail' tabIndex={-1} aria-label={tr('paperDetails')}>
      <div className='paper-workflow-heading'><h3>{selectedPaper.paper.title || tr('untitled')}</h3><button type='button' className='paper-workflow-text-button' onClick={() => setSelected(undefined)}>{tr('closeDetail')}</button></div>
      {selectedPaper.is_seed && <dl className='paper-workflow-analysis-fields'><div><dt>{tr('lineagePosition')}</dt><dd>{tr('seed')}</dd></div></dl>}
      {placement && <dl className='paper-workflow-analysis-fields'>
        <div><dt>{tr('lineagePosition')}</dt><dd>{direction?.name}</dd></div>
        {placement.branch_question && <div><dt>{tr('branchQuestion')}</dt><dd>{placement.branch_question}</dd></div>}
        {placement.reason && <div><dt>{tr('branchReason')}</dt><dd>{placement.reason}</dd></div>}
      </dl>}
      {renderDetail(selectedPaper)}
      {!!associated.length && <div className='paper-lineage-associated'><h4>{tr('associatedPapers')}</h4>{associated.map(relation => <div key={relation.id}>
        {paperLink(relation.source_paper_id === selected ? relation.target_paper_id : relation.source_paper_id)} <Tag>{tr(`relationType_${relation.relation_type}`)}</Tag>
        <p>{relation.statement}</p>{relation.conditions && <p className='paper-workflow-muted'>{relation.conditions}</p>}
        <ul>{relation.evidence.map((item, index) => <li key={index}>{paperLink(item.paper_id)} · <EvidencePosition evidence={item} /></li>)}</ul>
      </div>)}</div>}
    </div>}
    <details ref={evidence} className='paper-workflow-notes'><summary>{tr('relationTable')} ({relations.length})</summary>
      {relations.length ? <div className='paper-lineage-table-wrap'><table className='paper-lineage-table'><thead><tr><th>{tr('associatedPapers')}</th><th>{tr('relationType')}</th><th>{tr('relationStatement')}</th><th>{tr('relationEvidence')}</th></tr></thead>
        <tbody>{relations.map(relation => <tr key={relation.id}><td>{paperLink(relation.source_paper_id)}<br />{isDirectedPaperRelation(relation.relation_type) ? '→' : '—'} {paperLink(relation.target_paper_id)}</td><td>{tr(`relationType_${relation.relation_type}`)}</td><td>{relation.statement}{relation.conditions && <p className='paper-workflow-muted'>{relation.conditions}</p>}</td><td>{relation.evidence.map((item, index) => <div key={index}>{paperLink(item.paper_id)} · <EvidencePosition evidence={item} /></div>)}</td></tr>)}</tbody></table></div>
        : <p className='paper-workflow-muted'>{tr('noConfirmedRelations')}</p>}
    </details>
  </div>;
}
