import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react';
import { ArrowLeftOutlined, DownOutlined, ReadOutlined, RightOutlined } from '@ant-design/icons';
import { Segmented } from 'antd';
import { useTranslation } from 'react-i18next';
import type { WorkflowPaper } from '@/modules/knowledge/api/paperWorkflows';
import MarkdownViewer from '../MarkdownViewer';
import PaperGraph from './PaperGraph';
import { paperRelationEvidence, paperReportDiagrams, paperReportNarrative, visiblePaperAnalysis } from './paperReport';
import './PaperExpansionArtifactView.scss';

const paperSlots = new Set(['expansion_plan', 'candidate_set', 'relation_analysis', 'expansion_result', 'expansion_report']);
export const isPaperExpansionSlot = (slotId: string) => paperSlots.has(slotId);

type RecordValue = Record<string, unknown>;
const record = (value: unknown): RecordValue | null => value && typeof value === 'object' && !Array.isArray(value)
  ? value as RecordValue : null;
const text = (value: unknown) => typeof value === 'string' ? value : '';
const strings = (value: unknown): string[] => Array.isArray(value) ? value.filter(item => typeof item === 'string' && item.trim()) : [];
const numbers = (value: unknown): number[] => Array.isArray(value)
  ? [...new Set(value.filter((item): item is number => Number.isInteger(item) && item >= 0))] : [];

export function parsePaperExpansionArtifact(value: unknown): RecordValue | null {
  try {
    const parsed = typeof value === 'string' ? JSON.parse(value) : value;
    const data = record(parsed);
    return data && 'data' in data ? parsePaperExpansionArtifact(data.data) : data;
  } catch {
    return null;
  }
}

function papersIn(value: unknown): WorkflowPaper[] | null {
  const data = record(value);
  if (!data || !Array.isArray(data.papers)) return null;
  if (!data.papers.every(item => record(item) && typeof record(item.paper)?.paper_id === 'string')) return null;
  return data.papers as WorkflowPaper[];
}

export function canDisplayPaperExpansion(slotId: string, value: unknown): boolean {
  const data = parsePaperExpansionArtifact(value);
  if (!data) return false;
  if (slotId === 'expansion_report') return typeof data.narrative === 'string';
  const input = slotId === 'relation_analysis' ? record(data.input) : data;
  return isPaperExpansionSlot(slotId) && typeof input?.user_instruction === 'string' && papersIn(input) !== null;
}

function Section({ title, children, aside }: { title: string; children: ReactNode; aside?: string }) {
  return <section className='paper-workflow-section'>
    <div className='paper-workflow-heading'><h3>{title}</h3>{aside && <span>{aside}</span>}</div>
    {children}
  </section>;
}

function TextList({ items, title, ordered = false, note = false }: {
  items: unknown; title: string; ordered?: boolean; note?: boolean;
}) {
  const values = strings(items);
  if (!values.length) return null;
  const List = ordered ? 'ol' : 'ul';
  return <div className={note ? 'paper-workflow-note' : undefined}>
    <Section title={title}><List>{values.map((value, index) => <li key={index}>{value}</li>)}</List></Section>
  </div>;
}

function PaperTitle({ item }: { item: WorkflowPaper }) {
  const { t } = useTranslation();
  const paper = item.paper;
  const metadata = [strings(paper.authors).join('、'), typeof paper.year === 'number' ? paper.year : '', text(paper.venue), text(paper.source_provider)].filter(Boolean).join(' · ');
  return <div className='paper-workflow-paper-title'>
    <div>{text(paper.title) || t('paperTasks.workflowResult.untitled')}<span className='paper-workflow-tag'>
      {t(`paperTasks.workflowResult.${item.is_seed ? 'seed' : 'expanded'}`)}
    </span></div>
    {metadata && <p className='paper-workflow-muted'>{metadata}</p>}
  </div>;
}

function Reading({ value }: { value: unknown }) {
  const { t } = useTranslation();
  const reading = record(value);
  const level = text(reading?.read_level);
  return <div className='paper-workflow-reading'><ReadOutlined aria-hidden />
    {t(`paperTasks.workflowResult.${!level ? 'noReading' : level === 'full' ? 'readFull' : 'readSections'}`)}
    {typeof reading?.total_blocks === 'number' && <span> · {t('paperTasks.workflowResult.readCount', {
      count: numbers(reading.positions).length, total: reading.total_blocks,
    })}</span>}
  </div>;
}

function PaperDetail({ item, analysis, reading }: { item: WorkflowPaper; analysis: boolean; reading?: unknown }) {
  const { t } = useTranslation();
  const fields = ['seed_relation', 'direction_relation', 'key_contribution'] as const;
  const visible = fields.filter(field => visiblePaperAnalysis(item[field]));
  return <article className='paper-workflow-detail'>
    <PaperTitle item={item} />
    {analysis && <Reading value={record(reading)?.[item.paper.paper_id]} />}
    {analysis && visible.length ? visible.map(field => <Section key={field} title={t(`paperTasks.workflowResult.${field}`)}>
      <p>{visiblePaperAnalysis(item[field])}</p>
    </Section>) : text(item.paper.abstract)
      ? <Section title={t('paperTasks.workflowResult.abstract')}><p>{text(item.paper.abstract)}</p></Section>
      : <p className='paper-workflow-muted'>{t(`paperTasks.workflowResult.${analysis ? 'noAnalysis' : 'noAbstract'}`)}</p>}
  </article>;
}

function PaperList({ papers, onSelect }: { papers: WorkflowPaper[]; onSelect: (paper: WorkflowPaper) => void }) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  if (!papers.length) return <p className='paper-workflow-muted'>{t('paperTasks.workflowResult.noPapers')}</p>;
  return <div className='paper-workflow-paper-list'>
    {(expanded ? papers : papers.slice(0, 6)).map((item, index) => <button type='button' className='paper-workflow-paper-row'
      key={item.paper.paper_id} onClick={() => onSelect(item)}>
      <span className='paper-workflow-number'>{String(index + 1).padStart(2, '0')}</span>
      <span>{text(item.paper.title) || t('paperTasks.workflowResult.untitled')}</span>
      {item.is_seed && <span className='paper-workflow-tag'>{t('paperTasks.workflowResult.seed')}</span>}
      <RightOutlined aria-hidden />
    </button>)}
    {papers.length > 6 && <button type='button' className='paper-workflow-more' onClick={() => setExpanded(!expanded)}>
      {expanded ? t('paperTasks.workflowResult.collapsePapers') : t('paperTasks.workflowResult.morePapers', { count: papers.length - 6 })}
      <DownOutlined rotate={expanded ? 180 : 0} aria-hidden />
    </button>}
  </div>;
}

/** A read-only projection of publisher fields, including diagrams stored in the report Markdown. */
export function PaperExpansionArtifactView({ slotId, data, result, report, resultUnavailable = false }: {
  slotId: string; data: RecordValue; result?: unknown; report?: unknown; resultUnavailable?: boolean;
}) {
  const { t } = useTranslation();
  const tr = (key: string) => t(`paperTasks.workflowResult.${key}`);
  const id = useId();
  const [tab, setTab] = useState('');
  const [detail, setDetail] = useState<WorkflowPaper>();
  const [showEvidence, setShowEvidence] = useState(false);
  const [process, setProcess] = useState(false);
  const [readingView, setReadingView] = useState('map');
  const scroll = useRef<HTMLDivElement>(null);
  const isReport = slotId === 'expansion_report';
  const isPlan = slotId === 'expansion_plan';
  const analysis = slotId === 'relation_analysis' || slotId === 'expansion_result' || isReport;
  const input = slotId === 'relation_analysis' ? record(data.input) : isReport ? record(result) : data;
  const papers = papersIn(input) ?? [];
  const narrative = text(isReport ? data.narrative : record(report)?.narrative);
  const diagrams = useMemo(() => paperReportDiagrams(narrative), [narrative]);
  const reportNarrative = useMemo(() => paperReportNarrative(narrative), [narrative]);
  const relationEvidence = useMemo(() => paperRelationEvidence(narrative), [narrative]);
  const directions = strings(input?.selected_directions);
  const plan = strings(input?.plan);
  const decisions = strings(input?.decisions);
  const gaps = strings(input?.gaps);
  const reading = data.reading;
  const order = strings(data.reading_order);
  const byId = new Map(papers.map(item => [item.paper.paper_id, item]));
  const tabs = isReport ? [
    { key: 'reading', label: tr('readingMap'), count: order.length },
    { key: 'report', label: tr('narrative') },
    { key: 'limitations', label: tr('limitations'), count: strings(data.limitations).length },
  ] : isPlan ? [
    { key: 'scope', label: tr('scope') }, { key: 'plan', label: tr('plan'), count: plan.length },
    { key: 'papers', label: tr('plannedPapers'), count: papers.length },
  ] : analysis ? [
    ...(diagrams.relations ? [{ key: 'relations', label: tr('relationGraph') }] : []),
    { key: 'papers', label: tr('analysis'), count: papers.length },
  ] : [
    { key: 'papers', label: tr('papers'), count: papers.length },
    { key: 'decisions', label: tr('decisions'), count: decisions.length },
    { key: 'gaps', label: tr('gaps'), count: gaps.length },
  ];
  const active = tabs.find(item => item.key === tab) ?? tabs[0];
  const graphActive = !detail && !showEvidence && !process && (active.key === 'relations'
    || (active.key === 'reading' && diagrams.reading && readingView === 'map'));
  useEffect(() => { scroll.current?.scrollTo?.(0, 0); }, [active.key, detail, showEvidence, process]);
  const selectTab = (key: string) => { setTab(key); setDetail(undefined); setShowEvidence(false); setProcess(false); };
  const nodeLinks = Object.fromEntries(papers.map(item => [`p_${item.paper.paper_id}`, {
    title: text(item.paper.title) || tr('untitled'),
    label: t('paperTasks.workflowResult.viewPaper', { title: text(item.paper.title) || tr('untitled') }),
    onClick: () => setDetail(item),
  }]));
  return <div className='paper-workflow-result'>
    <div className='paper-workflow-heading paper-workflow-summary'><h3>{tr(isPlan ? 'scope' : isReport ? 'narrative' : analysis ? (diagrams.relations ? 'relationGraph' : 'analysis') : 'papers')}</h3>
      <span>{t('paperTasks.workflowResult.paperSummary', { count: papers.length, seeds: papers.filter(item => item.is_seed).length })}</span>
    </div>
    <div className='paper-workflow-tabs' role='tablist' aria-label={tr('views')} onKeyDown={event => {
      const index = tabs.indexOf(active);
      const next = event.key === 'ArrowRight' ? (index + 1) % tabs.length : event.key === 'ArrowLeft' ? (index + tabs.length - 1) % tabs.length
        : event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : -1;
      if (next < 0) return;
      event.preventDefault(); selectTab(tabs[next].key);
      document.getElementById(`${id}-${tabs[next].key}`)?.focus();
    }}>
      {tabs.map(item => <button type='button' key={item.key} role='tab' id={`${id}-${item.key}`}
        aria-selected={item.key === active.key && !process} tabIndex={item.key === active.key ? 0 : -1}
        aria-controls={`${id}-content`} onClick={() => selectTab(item.key)}>
        <span>{item.label}</span>{item.count !== undefined && <small>{item.count}</small>}
      </button>)}
    </div>
    <div ref={scroll} className={`paper-workflow-scroll${graphActive ? ' paper-workflow-scroll--graph' : ''}`} id={`${id}-content`} role='tabpanel' tabIndex={0}
      aria-labelledby={process ? `${id}-process` : `${id}-${active.key}`}>
      {detail || showEvidence ? <>
        <button type='button' className='paper-workflow-back' onClick={() => { setDetail(undefined); setShowEvidence(false); }}>
          <ArrowLeftOutlined aria-hidden />{tr('back')}
        </button>
        {detail ? <PaperDetail item={detail} analysis={analysis} reading={reading} /> : <MarkdownViewer>{relationEvidence}</MarkdownViewer>}
      </> : process ? <TextList title={tr('nextPlan')} items={plan} ordered /> : <>
        {active.key === 'scope' && <div className='paper-workflow-scope'>
          <div className='paper-workflow-goal'><small>{tr('goal')}</small><p>{text(input?.user_instruction)}</p></div>
          <div className='paper-workflow-branches'>{directions.map((direction, index) => <div key={direction}
            className={`paper-workflow-branch paper-workflow-tone-${index % 5}`}><small>{tr('directions')} {index + 1}</small><p>{direction}</p></div>)}</div>
        </div>}
        {active.key === 'plan' && <><TextList title={tr('plan')} items={plan} ordered />
          <TextList title={tr('selection')} items={decisions} /><TextList title={tr('gaps')} items={gaps} note /></>}
        {active.key === 'papers' && <PaperList key={slotId} papers={papers} onSelect={setDetail} />}
        {active.key === 'decisions' && (decisions.length ? <TextList title={tr('decisions')} items={decisions} /> : <p className='paper-workflow-muted'>{tr('emptyNotes')}</p>)}
        {active.key === 'gaps' && (gaps.length ? <TextList title={tr('gaps')} items={gaps} note /> : <p className='paper-workflow-muted'>{tr('emptyNotes')}</p>)}
        {active.key === 'relations' && diagrams.relations && <><p className='paper-workflow-muted'>{tr('graphHint')}</p>
          <div className='paper-workflow-legend'><span><i aria-hidden />{tr('methodLine')}</span><span><i className='is-comparison' aria-hidden />{tr('comparisonLine')}</span></div>
          <PaperGraph code={diagrams.relations} nodeLinks={nodeLinks} paperCount={papers.length}
            onViewEvidence={relationEvidence ? () => setShowEvidence(true) : undefined} /></>}
        {active.key === 'report' && <MarkdownViewer>{reportNarrative}</MarkdownViewer>}
        {active.key === 'reading' && <>
          {diagrams.reading && <Segmented aria-label={tr('readingDisplay')} value={readingView}
            options={[{ label: tr('readingMap'), value: 'map' }, { label: tr('readingList'), value: 'list' }]}
            onChange={(value: string | number) => setReadingView(String(value))} />}
          <p className='paper-workflow-muted'>{tr(diagrams.reading && readingView === 'map' ? 'readingMapHint' : 'readingHint')}</p>
          {diagrams.reading && readingView === 'map' ? <PaperGraph code={diagrams.reading} nodeLinks={nodeLinks} paperCount={order.length} reading /> : <>
          <ol className='paper-workflow-reading-order'>{order.map((paperId, index) => {
            const item = byId.get(paperId);
            return <li key={`${paperId}-${index}`}><span className='paper-workflow-route-number'>{index + 1}</span>
              {item ? <button type='button' onClick={() => setDetail(item)}><PaperTitle item={item} /><RightOutlined aria-hidden /></button>
                : <span className='paper-workflow-muted'>{tr('titleUnavailable')}</span>}</li>;
          })}</ol>
          {!order.length && <p className='paper-workflow-muted'>{tr('emptyNotes')}</p>}
          </>}
          {resultUnavailable && <p className='paper-workflow-muted'>{tr('resultUnavailable')}</p>}
        </>}
        {active.key === 'limitations' && (strings(data.limitations).length ? <TextList title={tr('limitations')} items={data.limitations} note /> : <p className='paper-workflow-muted'>{tr('emptyNotes')}</p>)}
      </>}
    </div>
    {!isPlan && !analysis && !!plan.length && <button type='button' id={`${id}-process`} className='paper-workflow-process'
      aria-expanded={process} aria-controls={`${id}-content`} onClick={() => { setProcess(!process); setDetail(undefined); }}>
      <span>{tr('nextPlan')}<small>{plan.length}</small></span><DownOutlined rotate={process ? 180 : 0} aria-hidden />
    </button>}
  </div>;
}

