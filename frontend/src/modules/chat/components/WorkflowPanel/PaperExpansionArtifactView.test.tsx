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
  expect(screen.getByText('Toolformer')).not.toBeVisible();
  fireEvent.click(screen.getByText('完整论文清单 · 2 篇'));
  expect(screen.getByRole('region', { name: '起始论文' })).toHaveTextContent('Toolformer');
  const candidates = screen.getByRole('region', { name: '候选论文' });
  expect(candidates).toHaveTextContent('ToolLLM');
  fireEvent.click(within(candidates).getByText('ToolLLM'));
  expect(within(candidates).getByText('暂无摘要。')).toBeVisible();
  expect(screen.queryByText('hidden search explanation')).not.toBeInTheDocument();
  expect(screen.queryByText('hidden gap')).not.toBeInTheDocument();
  expect(screen.queryByText('不展示内部前序调查问题')).not.toBeInTheDocument();
});

it.each([['fast', '候选论文'], ['balanced', '训练'], ['deep', '训练']])('shows the %s list with its saved recommendation', async (mode, section) => {
  show(slot('candidate_set', { ...input, list_mode: mode, recommendations: [
    { paper_id: 'paper-b', reason: '比较不同工具选择方法', ...(mode !== 'fast' ? { direction: '训练' } : {}) },
  ] }));
  const paper = await screen.findByText('ToolLLM');
  if (mode === 'fast') {
    expect(paper).toBeVisible();
  } else {
    expect(paper).not.toBeVisible();
    fireEvent.click(screen.getByText('完整论文清单 · 2 篇'));
  }
  const list = await screen.findByRole('region', { name: section });
  expect(list).toHaveTextContent('ToolLLM');
  expect(list).toHaveTextContent('比较不同工具选择方法');
  fireEvent.click(screen.getByText('完整论文清单 · 2 篇'));
  expect(paper).not.toBeVisible();
  fireEvent.click(screen.getByText('完整论文清单 · 2 篇'));
  expect(paper).toBeVisible();
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


it('shows the relationship graph above the research report and exposes its prose as source', async () => {
  const report = slot('expansion_report', { input,
    narrative: '## 研究概述\n\n工具选择依赖训练信号。[1]\n\n## 方法比较\n\n两种方法的训练数据和选择机制不同。',
    analysis_version: 'v2', relations: [{ id: 'relation', source_paper_id: 'paper-a', target_paper_id: 'paper-b',
      relation_type: 'mechanism_comparison', statement: '两种工具选择机制不同', conditions: '', evidence: [] }],
    investigation: [{ conclusion: 'internal investigation log' }] });
  show(report);
  const article = await screen.findByRole('article');
  expect(within(article).getByRole('heading', { name: '研究概述' })).toBeVisible();
  expect(within(article).getByRole('heading', { name: '方法比较' })).toBeVisible();
  const graph = screen.getByLabelText('关系图');
  expect(graph).toBeVisible();
  expect(graph.compareDocumentPosition(article) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  fireEvent.click(within(graph).getByRole('button', { name: '查看关系依据' }));
  expect(screen.getByText('两种工具选择机制不同')).toBeVisible();
  expect(screen.queryByText('internal investigation log')).not.toBeInTheDocument();
  expect(screen.queryByText(/完整论文清单/)).not.toBeInTheDocument();
  fireEvent.click(screen.getByText('展示源码'));
  expect(screen.queryByText(/"analysis_version"/)).not.toBeInTheDocument();
});

it('preserves unknown payloads on the original renderer', async () => {
  show(slot('relation_analysis', { input: null, unexpected: '保留原数据' }));
  expect(await screen.findByText(/"unexpected"/)).toBeVisible();
});

it('keeps candidate lists compact while allowing every saved paper to be opened', async () => {
  const candidates = Array.from({ length: 9 }, (_, index) => ({ ...papers[1], paper: { ...papers[1].paper, paper_id: `candidate-${index}`, title: `Candidate ${index}` } }));
  show(slot('candidate_set', { ...input, papers: [papers[0], ...candidates] }));
  expect(await screen.findByText('Candidate 5')).not.toBeVisible();
  expect(screen.queryByText('研究范围')).not.toBeInTheDocument();
  fireEvent.click(screen.getByText('完整论文清单 · 10 篇'));
  expect(screen.getByText('Candidate 8')).toBeVisible();
  fireEvent.click(screen.getByText('完整论文清单 · 10 篇'));
  expect(screen.getByText('Candidate 8')).not.toBeVisible();
});

it.each([
  ['complete', 'scope_covered', '研究完成', undefined],
  ['incomplete', 'budget_exhausted', '研究部分完成', '本轮研究额度已用尽，部分问题尚未完成调查。'],
  ['incomplete', 'material_limited', '研究部分完成', '已有材料尚不足以覆盖全部研究问题，未覆盖部分已在报告中说明。'],
])('shows saved research completion: %s / %s', async (status, reason, label, explanation) => {
  show(slot('expansion_report', { analysis_version: 'checked', input, narrative: '研究正文', relations: [], completion: { status, reason } }));
  expect(await screen.findByText(label)).toBeVisible();
  if (explanation) expect(screen.getByText(explanation)).toBeVisible();
  expect(screen.getByText('研究正文')).toBeVisible();
});


it.each(['fast', 'balanced', 'deep'])('shows saved screening changes for %s without using a newer candidate list', async mode => {
  const added = { ...papers[1], paper: { ...papers[1].paper, paper_id: 'new', title: 'New source' } };
  const screened = slot('screened_candidates', { ...input, papers: [papers[0], added], list_mode: mode,
    recommendations: [{ paper_id: 'new', direction: '训练', reason: '补齐方法来源' }],
    selection: { before_count: 2, changes: [
      { action: 'added', paper: added, reason: '补齐方法来源' },
      { action: 'removed', paper: papers[1], reason: '研究对象不符' },
    ] },
  });
  show(screened, [screened, slot('candidate_set', { ...input, papers: [] })]);
  const changes = await screen.findByRole('region', { name: /^候选 .* 篇 · 剔除/ });
  expect(changes).toHaveTextContent('候选 2 篇 · 剔除 1 篇 · 补入 1 篇 · 最终 2 篇');
  expect(within(changes).getByRole('heading', { name: '候选 2 篇 · 剔除 1 篇 · 补入 1 篇 · 最终 2 篇' })).toBeVisible();
  expect(within(changes).queryByText('本步筛选与补充')).not.toBeInTheDocument();
  expect(within(changes).queryByText('本步补入 1 篇')).not.toBeInTheDocument();
  expect(changes).toHaveTextContent('New source');
  expect(within(changes).getByText('补齐方法来源')).toBeVisible();
  fireEvent.click(within(changes).getByText('本步剔除 1 篇'));
  expect(within(changes).getByText('研究对象不符')).toBeVisible();
  if (mode === 'fast') {
    expect(screen.getByText('Toolformer')).toBeVisible();
  } else {
    expect(screen.getByText('Toolformer')).not.toBeVisible();
    fireEvent.click(screen.getByText('完整论文清单 · 2 篇'));
  }
  expect(screen.getByRole('region', { name: mode === 'fast' ? '候选论文' : '训练' })).toHaveTextContent('New source');
});

it('derives historical screening changes from the earlier candidate snapshot without inventing reasons', async () => {
  const added = { ...papers[1], paper: { ...papers[1].paper, paper_id: 'new', title: 'New source' } };
  const screened = slot('screened_candidates', { ...input, papers: [papers[0], added], list_mode: 'balanced',
    recommendations: [{ paper_id: 'new', direction: '训练', reason: '已有推荐理由' }],
  });
  show(screened, [screened, slot('candidate_set', input)]);
  const changes = await screen.findByRole('region', { name: /^候选 .* 篇 · 剔除/ });
  expect(changes).toHaveTextContent('候选 2 篇 · 剔除 1 篇 · 补入 1 篇 · 最终 2 篇');
  expect(changes).toHaveTextContent('已有推荐理由');
  fireEvent.click(within(changes).getByText('本步剔除 1 篇'));
  expect(within(changes).getByText('未记录剔除理由。')).toBeVisible();
});

it('does not compare an old screening result with candidates from a later run', async () => {
  const screened = { ...slot('screened_candidates', { ...input, list_mode: 'balanced' }), created_at: '2026-11-03T10:00:00Z' };
  const later = { ...slot('candidate_set', { ...input, papers: [] }), created_at: '2026-11-03T11:00:00Z' };
  show(screened, [screened, later]);
  expect(await screen.findByText('暂无法获取本步增删明细。')).toBeVisible();
  expect(screen.queryByRole('region', { name: /^候选 .* 篇 · 剔除/ })).not.toBeInTheDocument();
});

it('keeps the relationship graph in analysis with paper details and citations, without verification controls', async () => {
  show(slot('relation_analysis', { input, selection: { before_count: 2, changes: [] }, relations: [{ id: 'methods', source_paper_id: 'paper-a', target_paper_id: 'paper-b',
    relation_type: 'mechanism_comparison', statement: '两种工具选择机制不同', conditions: '在各自训练设置下',
    evidence: [{ paper_id: 'paper-a', positions: [0], source_version: 'v1' }] }] }));
  expect(await screen.findByLabelText('关系图')).toHaveAttribute('data-edges', 'methods');
  expect(screen.queryByRole('switch')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '查看论文：Toolformer' }));
  const detail = screen.getByLabelText('论文详情');
  expect(within(detail).getByText('Original abstract')).toBeVisible();
  expect(within(detail).getByText('两种工具选择机制不同')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: '查看关系依据' }));
  expect(screen.getByRole('table')).toHaveTextContent('两种工具选择机制不同');
  expect(screen.queryByText('显示待核验关系')).not.toBeInTheDocument();
});
