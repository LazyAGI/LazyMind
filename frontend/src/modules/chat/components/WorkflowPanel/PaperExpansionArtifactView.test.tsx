import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { SlotRevision, WorkflowSession } from '@/modules/chat/store/workflowPanel';
import { SlotRenderer } from './SlotComponents';
vi.mock('./FilePreviewDrawer', () => ({ FilePreviewDrawer: () => null }));
vi.mock('./PaperGraph', () => ({ default: ({ graph, nodeLinks, onViewEvidence }: {
  graph: { nodes: Array<{ id: string }>; edges: Array<{ id: string }> }; nodeLinks: Record<string, { label: string; title?: string; subtitle?: string; onClick: () => void }>; onViewEvidence?: () => void;
}) => <div aria-label='关系图' data-edges={graph.edges.map(item => item.id).join(',')}>{Object.entries(nodeLinks).map(([id, link]) => <button key={id} aria-label={link.label} title={link.title} onClick={link.onClick}>{link.label}{link.subtitle && <small>{link.subtitle}</small>}</button>)}
  {onViewEvidence && <button onClick={onViewEvidence}>查看关系依据</button>}</div> }));
const papers = [
  { paper: { paper_id: 'paper-a', title: 'Toolformer', authors: ['Author'], year: 2023, abstract: 'Original abstract' }, is_seed: true, is_important: false },
  { paper: { paper_id: 'paper-b', title: 'ToolLLM', abstract: null }, is_seed: false, is_important: false },
];
const input = { user_instruction: '研究工具使用', candidate_directions: ['训练', '推理'], selected_directions: ['训练'], papers };
const slot = (name: string, data: unknown): SlotRevision => ({ slot_id: name, slot: name, selected: true, revision: 1, revision_count: 1, created_at: '', content_type: 'json', artifact_value: { data } });
function show(revision: SlotRevision, slots = [revision], workflowId = 'paper_expansion') {
  const session: WorkflowSession = { session_id: 'paper-session', conversation_id: 'paper-chat', workflow_id: workflowId,
    workflow_mode: 'auto', status: 'completed', current_step_id: 'analyze_relations', created_at: '', updated_at: '', slots };
  return render(<SlotRenderer slot={revision} slotId={revision.slot} sessionId='paper-session' workflowSession={session}
    widget={{ widgetType: 'json-block', readOnly: true }} revisionCount={1} />);
}
beforeEach(async () => {
  await i18n.changeLanguage('zh-CN');
  HTMLElement.prototype.scrollIntoView = vi.fn();
});
afterEach(() => vi.unstubAllGlobals());

it('shows confirmed research questions, separate starting papers and candidate abstracts', async () => {
  show(slot('candidate_set', { ...input, directions: [{ name: '训练', question: '训练信号如何影响工具选择？' }], research_questions: [{ id: 'q', direction: '训练', question: '不展示内部前序调查问题' }], decisions: ['hidden search explanation'], gaps: ['hidden gap'] }));
  expect(await screen.findByText('训练信号如何影响工具选择？')).toBeVisible();
  expect(screen.getByRole('region', { name: '起始论文' })).toHaveTextContent('Toolformer');
  const candidates = screen.getByRole('region', { name: '候选论文' });
  expect(candidates).toHaveTextContent('ToolLLM');
  fireEvent.click(within(candidates).getByText('ToolLLM'));
  expect(within(candidates).getByText('暂无摘要。')).toBeVisible();
  expect(screen.queryByText('hidden search explanation')).not.toBeInTheDocument();
  expect(screen.queryByText('hidden gap')).not.toBeInTheDocument();
  expect(screen.queryByText('不展示内部前序调查问题')).not.toBeInTheDocument();
});

it.each([['fast', '完整论文列表'], ['balanced', '训练']])('shows the %s list with its saved recommendation', async (mode, section) => {
  show(slot('candidate_set', { ...input, list_mode: mode, recommendations: [
    { paper_id: 'paper-b', reason: '比较不同工具选择方法', ...(mode === 'balanced' ? { direction: '训练' } : {}) },
  ] }));
  const list = await screen.findByRole('region', { name: section });
  expect(list).toHaveTextContent('ToolLLM');
  expect(list).toHaveTextContent('比较不同工具选择方法');
  expect(screen.queryByLabelText('关系图')).not.toBeInTheDocument();
});

it('uses saved selection changes rather than the currently selected upstream list', async () => {
  const added = { ...papers[1], paper: { ...papers[1].paper, paper_id: 'new', title: 'New paper' } };
  show(slot('relation_analysis', { input: { ...input, papers: [papers[0], added] }, selection: { before_count: 2, changes: [
    { action: 'added', paper: added, reason: '补充工具选择机制' }, { action: 'removed', paper: papers[1], reason: '与已确认方向无关' },
  ] } }));
  expect(await screen.findByText('原有 2 篇 · 保留 1 篇 · 新增 1 篇 · 移除 1 篇')).toBeVisible();
  expect(screen.getByText('补充工具选择机制')).toBeVisible();
  expect(screen.getByText('与已确认方向无关')).not.toBeVisible();
  fireEvent.click(screen.getByText('已移除 1 篇'));
  expect(screen.getByText('与已确认方向无关')).toBeVisible();
  expect(screen.getByText('Original abstract')).not.toBeVisible();
});


it('shows the frozen relationship network before the report, with pending relationships and evidence access', async () => {
  const report = slot('expansion_report', { input, narrative: '研究正文', analysis_version: 'v2', reading_order: ['paper-a'],
    reading_plan: [{ paper_id: 'paper-a', purpose: 'obsolete reading route' }],
    relations: [{ id: 'pending', source_paper_id: 'paper-a', target_paper_id: 'paper-b', relation_type: 'mechanism_comparison', statement: '待核验的机制对照', evidence: [], verification: { status: 'uncertain' } }] });
  show(report, [report, slot('relation_analysis', { input, analysis_version: 'v1', relations: [] })]);
  expect(await screen.findByLabelText('关系图')).toHaveAttribute('data-edges', 'pending');
  expect(screen.getByText('已核验 0 条 · 待核验 1 条')).toBeVisible();
  expect(within(screen.getByLabelText('关系图')).getByText('起始论文 · 2023')).toBeVisible();
  expect(screen.getByRole('button', { name: /查看论文：Toolformer/ })).toHaveAttribute('title', 'Toolformer');
  expect(screen.getByText('研究正文')).toBeVisible();
  expect(screen.queryByText('obsolete reading route')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '查看论文：Toolformer' }));
  expect(within(screen.getByLabelText('论文详情')).getByText('Original abstract')).toBeVisible();
  fireEvent.click(screen.getByRole('switch'));
  expect(screen.getByLabelText('关系图')).toHaveAttribute('data-edges', '');
  fireEvent.click(screen.getByRole('button', { name: '查看关系依据' }));
  expect(screen.getByRole('table')).toHaveTextContent('待核验的机制对照');
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('preserves unknown payloads on the original renderer', async () => {
  show(slot('relation_analysis', { input: null, unexpected: '保留原数据' }));
  expect(await screen.findByText(/"unexpected"/)).toBeVisible();
});

it('keeps candidate lists compact while allowing every saved paper to be opened', async () => {
  const candidates = Array.from({ length: 9 }, (_, index) => ({ ...papers[1], paper: { ...papers[1].paper, paper_id: `candidate-${index}`, title: `Candidate ${index}` } }));
  show(slot('candidate_set', { ...input, papers: [papers[0], ...candidates] }));
  expect(await screen.findByText('Candidate 5')).toBeVisible();
  expect(screen.queryByText('Candidate 6')).not.toBeInTheDocument();
  expect(screen.queryByText('研究范围')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '展开其余 3 篇' }));
  expect(screen.getByText('Candidate 8')).toBeVisible();
});

it('summarizes actual relationship verification timeouts without exposing investigation gaps', async () => {
  show(slot('expansion_report', { analysis_version: 'checked', input, narrative: '研究正文', coverage_gaps: [{ reason: '不展示的内部缺口' }], relations: [{
    id: 'timeout', source_paper_id: 'paper-a', target_paper_id: 'paper-b', relation_type: 'mechanism_comparison', statement: '未完成核验的关系', evidence: [],
    verification: { status: 'uncertain', verification_error: 'stage_timeout' },
  }] }));
  expect(await screen.findByRole('status')).toHaveTextContent('1 条关系核验超时，尚未确认');
  expect(screen.queryByText('不展示的内部缺口')).not.toBeInTheDocument();
});


it('excludes unsupported relationships from the graph while preserving their evidence and count', async () => {
  const relation = (id: string, status: string) => ({ id, source_paper_id: 'paper-a', target_paper_id: 'paper-b', relation_type: 'mechanism_comparison', statement: `${id}关系声明`, evidence: [], verification: { status, scope_status: status } });
  show(slot('expansion_report', { analysis_version: 'checked', input, narrative: '研究正文', relations: [relation('verified', 'supported'), relation('pending', 'uncertain'), relation('unsupported', 'unsupported')] }));
  expect(await screen.findByLabelText('关系图')).toHaveAttribute('data-edges', 'verified,pending');
  expect(screen.getByText(/证据不支持.*1/)).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: '查看论文：Toolformer' }));
  expect(within(screen.getByLabelText('论文详情')).getAllByText('候选关系声明，尚未核验。')).toHaveLength(1);
  fireEvent.click(screen.getByRole('switch', { name: '显示待核验关系' }));
  expect(screen.getByLabelText('关系图')).toHaveAttribute('data-edges', 'verified');
  fireEvent.click(screen.getByRole('button', { name: '查看关系依据' }));
  expect(within(screen.getByRole('table')).getByText('unsupported关系声明')).toBeVisible();
  expect(within(screen.getByRole('table')).getByText('证据不支持')).toBeVisible();
  fireEvent.click(screen.getByRole('switch', { name: '显示待核验关系' }));
  expect(screen.getByLabelText('关系图')).toHaveAttribute('data-edges', 'verified,pending');
});
