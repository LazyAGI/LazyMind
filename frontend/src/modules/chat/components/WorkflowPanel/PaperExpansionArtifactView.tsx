import { useMemo, type ReactNode } from 'react';
import { CheckOutlined, DownOutlined, ReadOutlined, RightOutlined } from '@ant-design/icons';
import { Tag, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import type { PaperInvestigation, PaperRelation, PaperResearchStructure, PaperRecommendation, PaperSelectionChanges, WorkflowPaper } from '@/modules/knowledge/api/paperWorkflows';
import MarkdownViewer from '../MarkdownViewer';
import PaperLineageView from './PaperLineageView';
import PaperResearchCompletion from './PaperResearchCompletion';
import { visiblePaperAnalysis } from './paperReport';
import './PaperExpansionArtifactView.scss';

const paperSlots = new Set(['candidate_set', 'screened_candidates', 'relation_analysis', 'expansion_result', 'expansion_report']);
export const isPaperExpansionSlot = (slotId: string) => paperSlots.has(slotId);
type RecordValue = Record<string, unknown>;
const record = (value: unknown): RecordValue | null => value && typeof value === 'object' && !Array.isArray(value) ? value as RecordValue : null;
const text = (value: unknown) => typeof value === 'string' ? value : '';
const strings = (value: unknown): string[] => Array.isArray(value) ? value.filter(item => typeof item === 'string' && item.trim()) : [];
const numbers = (value: unknown): number[] => Array.isArray(value) ? [...new Set(value.filter((item): item is number => Number.isInteger(item) && item >= 0))] : [];

export function parsePaperExpansionArtifact(value: unknown): RecordValue | null {
  try {
    const parsed = typeof value === 'string' ? JSON.parse(value) : value;
    const data = record(parsed);
    return data && 'data' in data ? parsePaperExpansionArtifact(data.data) : data;
  } catch { return null; }
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
  if (slotId === 'expansion_report') return typeof data.narrative === 'string' && papersIn(data.input) !== null
    && typeof data.analysis_version === 'string' && Array.isArray(data.relations);
  if (slotId === 'relation_analysis' && !record(data.selection)) return false;
  const input = slotId === 'relation_analysis' ? record(data.input) : data;
  return isPaperExpansionSlot(slotId) && typeof input?.user_instruction === 'string' && papersIn(input) !== null;
}
function Section({ title, children, aside, centered = false }: {
  title: string; children: ReactNode; aside?: string; centered?: boolean;
}) {
  return <section className='paper-workflow-section' aria-label={title}>
    <div className={`paper-workflow-heading${centered ? ' paper-workflow-heading--centered' : ''}`}>
      <h3>{title}</h3>{aside && <span>{aside}</span>}
    </div>{children}
  </section>;
}
function PaperTitle({ item, compact = false }: { item: WorkflowPaper; compact?: boolean }) {
  const { t } = useTranslation();
  const paper = item.paper;
  const authors = strings(paper.authors);
  const authorText = compact ? `${authors.slice(0, 2).join('、')}${authors.length > 2 ? '…' : ''}` : authors.join('、');
  const metadata = [typeof paper.year === 'number' ? paper.year : '', authorText, text(paper.venue)].filter(Boolean).join(' · ');
  return <div className='paper-workflow-paper-title'>
    <div>{text(paper.title) || t('paperTasks.workflowResult.untitled')}{item.is_seed && <span className='paper-workflow-tag'>{t('paperTasks.workflowResult.seed')}</span>}</div>
    {metadata && <p className='paper-workflow-muted'>{metadata}</p>}
  </div>;
}
function Reading({ value }: { value: unknown }) {
  const { t } = useTranslation();
  const reading = record(value);
  const level = text(reading?.read_level);
  return <div className='paper-workflow-reading'><ReadOutlined aria-hidden />
    {t(`paperTasks.workflowResult.${!level ? 'noReading' : level === 'full' ? 'readFull' : 'readSections'}`)}
    {typeof reading?.total_blocks === 'number' && <span> · {t('paperTasks.workflowResult.readCount', { count: numbers(reading.positions).length, total: reading.total_blocks })}</span>}
  </div>;
}
function PaperDetail({ item, analysis, reading }: { item: WorkflowPaper; analysis: boolean; reading?: unknown }) {
  const { t } = useTranslation();
  const fields = ['key_contribution', 'direction_relation', 'seed_relation'] as const;
  const visible = fields.filter(field => visiblePaperAnalysis(item[field]));
  const sourceUrl = item.paper.paper_url || item.paper.pdf_url;
  return <div className='paper-workflow-detail'>
    {sourceUrl && /^https?:\/\//i.test(sourceUrl) && <p><a href={sourceUrl} target='_blank' rel='noopener noreferrer'>{t('paperTasks.workflowResult.openPaperSource')}</a></p>}
    {(item.paper.published_at || item.paper.source_version_date) && <dl className='paper-workflow-dates'>
      {([['published_at', 'firstPublishedAt'], ['source_version_date', 'sourceVersionDate']] as const).map(([field, label]) => {
        const value = item.paper[field];
        return value ? <div key={field}><dt>{t(`paperTasks.workflowResult.${label}`)}</dt><dd><time dateTime={value}>{value.slice(0, 10)}</time></dd></div> : null;
      })}
    </dl>}
    {analysis && <Reading value={record(reading)?.[item.paper.paper_id]} />}
    {analysis && visible.length ? <dl className='paper-workflow-analysis-fields'>{visible.map(field => <div key={field}>
      <dt>{t(`paperTasks.workflowResult.${field}`)}</dt><dd>{visiblePaperAnalysis(item[field])}</dd>
    </div>)}</dl> : text(item.paper.abstract)
      ? <><h4>{t('paperTasks.workflowResult.abstract')}</h4><p>{text(item.paper.abstract)}</p></>
      : <p className='paper-workflow-muted'>{t(`paperTasks.workflowResult.${analysis ? 'noAnalysis' : 'noAbstract'}`)}</p>}
  </div>;
}
function PaperList({ papers, analysis = false, reading, recommendations }: {
  papers: WorkflowPaper[]; analysis?: boolean; reading?: unknown; recommendations?: Map<string, PaperRecommendation>;
}) {
  const { t } = useTranslation();
  if (!papers.length) return <p className='paper-workflow-muted'>{t('paperTasks.workflowResult.noPapers')}</p>;
  return <div className='paper-workflow-paper-list'>{papers.map((item, index) => <details
    className='paper-workflow-paper' key={item.paper.paper_id}>
    <summary className='paper-workflow-paper-row'><span className='paper-workflow-number'>{String(index + 1).padStart(2, '0')}</span>
      <div><PaperTitle item={item} compact />
        {recommendations?.get(item.paper.paper_id)?.reason && <p className='paper-workflow-muted'>{recommendations.get(item.paper.paper_id)?.reason}</p>}
      </div>
      <RightOutlined aria-hidden />
    </summary><PaperDetail item={item} analysis={analysis} reading={reading} />
  </details>)}</div>;
}

function SelectionChanges({ selection, screening = false, reading }: {
  selection: PaperSelectionChanges; screening?: boolean; reading?: unknown;
}) {
  const { t } = useTranslation();
  const tr = (key: string) => t(`paperTasks.workflowResult.${key}`);
  const additions = selection.changes.filter(change => change.action === 'added');
  const removals = selection.changes.filter(change => change.action === 'removed');
  const summary = t(`paperTasks.workflowResult.${screening ? 'screeningSummary' : 'selectionSummary'}`, {
    before: selection.before_count, retained: selection.before_count - removals.length, added: additions.length, removed: removals.length,
  });
  return <Section title={screening ? summary : tr('selectionChanges')} aside={screening ? undefined : summary} centered={screening}>
    {additions.length ? <>
      {!screening && <Tag color='blue' bordered={false}>{tr('addedPaper')}</Tag>}
      <PaperList papers={additions.map(change => change.paper)} analysis={!screening} reading={reading}
        recommendations={new Map(additions.map(change => [change.paper.paper.paper_id, {
          paper_id: change.paper.paper.paper_id, reason: change.reason,
        }]))} />
    </> : <p className='paper-workflow-muted'>{tr('noAddedPapers')}</p>}
    {removals.length ? <details className='paper-workflow-notes'><summary>{t(`paperTasks.workflowResult.${screening ? 'screenedOutPapers' : 'removedPapers'}`, { count: removals.length })}<DownOutlined aria-hidden /></summary>
      {removals.map(change => <div className='paper-workflow-change' key={change.paper.paper.paper_id}><PaperTitle item={change.paper} /><p>{change.reason || tr('reasonNotRecorded')}</p></div>)}
    </details> : screening && <p className='paper-workflow-muted'>{tr('noRemovedPapers')}</p>}
  </Section>;
}

/** Render only the saved artifact for this stage; selection changes are bound at publication. */
export function PaperExpansionArtifactView({ slotId, data, baseline }: {
  slotId: string; data: RecordValue; baseline?: RecordValue | null;
}) {
  const { t } = useTranslation();
  const tr = (key: string) => t(`paperTasks.workflowResult.${key}`);
  const isReport = slotId === 'expansion_report';
  const isAnalysis = slotId === 'relation_analysis';
  const input = isAnalysis || isReport ? record(data.input) : data;
  const papers = useMemo(() => [...new Map((papersIn(input) ?? []).map(item => [item.paper.paper_id, item])).values()], [input]);
  const seeds = papers.filter(item => item.is_seed);
  const candidates = papers.filter(item => !item.is_seed);
  const reading = data.reading;
  const narrative = useMemo(() => text(data.narrative), [data.narrative]);
  const selection = record(data.selection) as unknown as PaperSelectionChanges | null;
  const directions = strings(input?.selected_directions);
  const savedDirections = Array.isArray(data.directions) ? data.directions : record(data.research_structure)?.directions;
  const questions = Array.isArray(savedDirections) ? savedDirections.map(record).filter(item => item !== null) : [];
  const count = (value: number) => t('paperTasks.workflowResult.paperCount', { count: value });
  const listMode = (slotId === 'candidate_set' || slotId === 'screened_candidates') ? data.list_mode : undefined;
  const recommendations = new Map((Array.isArray(data.recommendations) ? data.recommendations as PaperRecommendation[] : [])
    .map(item => [item.paper_id, item]));
  const screeningSelection = useMemo(() => {
    if (slotId !== 'screened_candidates' || selection) return selection;
    const previous = papersIn(baseline);
    if (!previous) return null;
    const before = new Map(previous.map(item => [item.paper.paper_id, item]));
    const after = new Set(papers.map(item => item.paper.paper_id));
    const savedReasons = new Map((data.recommendations as PaperRecommendation[] | undefined)?.map(item => [item.paper_id, item.reason]));
    return { before_count: before.size, changes: [
      ...papers.filter(item => !before.has(item.paper.paper_id)).map(paper => ({
        action: 'added' as const, paper, reason: savedReasons.get(paper.paper.paper_id) ?? '',
      })),
      ...previous.filter(item => !after.has(item.paper.paper_id)).map(paper => ({ action: 'removed' as const, paper, reason: '' })),
    ] };
  }, [slotId, selection, baseline, papers, data.recommendations]);
  const grouped = directions.map(direction => {
    const group = candidates.filter(item => recommendations.get(item.paper.paper_id)?.direction === direction);
    return <Section key={direction} title={direction} aside={count(group.length)}>
      <PaperList papers={group} recommendations={recommendations} analysis={isReport} reading={reading} />
    </Section>;
  });
  const seedList = !!seeds.length && <Section title={tr('seedPapers')} aside={count(seeds.length)}><PaperList papers={seeds} analysis={isReport} reading={reading} /></Section>;
  const finalList = <details className='paper-workflow-notes'><summary>{tr('finalPapers')} · {count(papers.length)}<DownOutlined aria-hidden /></summary>
    {isReport && Array.isArray(data.recommendations) ? <>{seedList}{grouped}</> :
      <PaperList papers={papers} analysis reading={reading} recommendations={recommendations} />}</details>;
  return <div className='paper-workflow-result'><div className='paper-workflow-scroll'>
    {isReport ? <>
      <PaperResearchCompletion completion={data.completion as PaperInvestigation['completion']} />
      <article className='paper-workflow-report' aria-label={tr('narrative')}>
        <MarkdownViewer>{narrative}</MarkdownViewer>
      </article>
    </> : isAnalysis ? <>
      {Array.isArray(data.relations) && <PaperLineageView papers={papers} relations={data.relations as PaperRelation[]}
        structure={data.research_structure as PaperResearchStructure | undefined}
        renderDetail={item => <PaperDetail item={item} analysis reading={reading} />} />}
      {data.list_mode !== 'deep' && selection && <SelectionChanges selection={selection} reading={reading} />}
      {finalList}
    </> : listMode === 'fast' || listMode === 'balanced' || listMode === 'deep' ? <>
      {slotId === 'screened_candidates' && (screeningSelection
        ? <SelectionChanges selection={screeningSelection} screening />
        : <p className='paper-workflow-muted'>{tr('selectionUnavailable')}</p>)}
      <details className='paper-workflow-notes' open={listMode === 'fast'}><summary>{tr('finalPapers')} · {count(papers.length)}<DownOutlined aria-hidden /></summary>
        {seedList}
        {listMode === 'fast' ? <Section title={tr('candidatePapers')} aside={count(candidates.length)}>
          <PaperList papers={candidates} recommendations={recommendations} />
        </Section> : grouped}
      </details>
    </> : <>
      <Section title={tr('searchDirections')} aside={t('paperTasks.workflowResult.selectedDirectionCount', { count: directions.length })}><div className='paper-workflow-scope'>
        <ul className='paper-workflow-directions' aria-label={tr('confirmedDirections')}>{directions.map((direction, index) => <li key={direction} className='paper-workflow-direction'>
          <div className='paper-workflow-direction-top'><span>{String(index + 1).padStart(2, '0')}</span><span><CheckOutlined /> {tr('confirmedDirection')}</span></div><h4>{direction}</h4>
          {text(questions.find(question => question.name === direction)?.question) && <Typography.Paragraph ellipsis={{ rows: 3, expandable: 'collapsible', symbol: (expanded: boolean) => t(expanded ? 'paperTasks.overview.collapse' : 'paperTasks.overview.expand') }}>{text(questions.find(question => question.name === direction)?.question)}</Typography.Paragraph>}
        </li>)}</ul>
      </div></Section>
      <details className='paper-workflow-notes'><summary>{tr('finalPapers')} · {count(papers.length)}<DownOutlined aria-hidden /></summary>
        {seedList}
        <Section title={tr('candidatePapers')} aside={count(candidates.length)}><PaperList papers={candidates} /></Section>
      </details>
    </>}
  </div></div>;
}
