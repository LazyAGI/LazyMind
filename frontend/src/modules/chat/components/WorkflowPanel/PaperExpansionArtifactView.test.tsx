import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { SlotRevision, WorkflowSession } from '@/modules/chat/store/workflowPanel';
import { SlotRenderer } from './SlotComponents';
vi.mock('./FilePreviewDrawer', () => ({ FilePreviewDrawer: () => null }));
vi.mock('../MarkdownViewer/MermaidBlock', () => ({ default: ({ code, nodeLinks = {} }: {
  code: string; nodeLinks?: Record<string, { label: string; onClick: () => void }>;
}) => <div aria-label='关系图'>{code}{Object.entries(nodeLinks).map(([id, link]) => <button key={id} onClick={link.onClick}>{link.label}</button>)}</div> }));
vi.mock('./PaperGraph', () => ({ default: ({ code, nodeLinks }: {
  code: string; nodeLinks: Record<string, { label: string; onClick: () => void }>;
}) => <div aria-label='关系图'>{code}{Object.entries(nodeLinks).map(([id, link]) => <button key={id} onClick={link.onClick}>{link.label}</button>)}</div> }));
const papers = [
  { paper: { paper_id: 'paper-a', title: 'Toolformer', authors: ['Author'], year: 2023, abstract: 'Original abstract' }, is_seed: true, is_important: false },
  { paper: { paper_id: 'paper-b', title: 'ToolLLM', abstract: null }, is_seed: false, is_important: false },
];
const input = { user_instruction: '研究工具使用', selected_directions: ['训练'], papers };
const slot = (name: string, data: unknown): SlotRevision => ({ slot_id: name, slot: name, selected: true, revision: 1, revision_count: 1, created_at: '', content_type: 'json', artifact_value: { data } });
function show(revision: SlotRevision, slots = [revision], workflowId = 'paper_expansion') {
  const session: WorkflowSession = { session_id: 'paper-session', conversation_id: 'paper-chat', workflow_id: workflowId,
    workflow_mode: 'auto', status: 'completed', current_step_id: 'compose_result', created_at: '', updated_at: '', slots };
  return render(<SlotRenderer slot={revision} slotId={revision.slot} sessionId='paper-session' workflowSession={session}
    widget={{ widgetType: 'json-block', readOnly: true }} revisionCount={1} />);
}
beforeEach(async () => { await i18n.changeLanguage('zh-CN'); });
afterEach(() => vi.unstubAllGlobals());
it('separates scope, plan and papers while preserving source and versions', async () => {
  const data = { ...input, plan: ['检索代表论文'], decisions: ['保留用户参考论文'], gaps: ['评测条件尚未对齐'] };
  show(slot('expansion_plan', data));
  expect(await screen.findByText('研究工具使用')).toBeVisible();
  expect(screen.getByText('训练')).toBeVisible();
  expect(screen.queryByText('检索代表论文')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('tab', { name: /检索计划/ }));
  expect(screen.getByText('检索代表论文')).toBeVisible();
  expect(screen.getByText('保留用户参考论文')).toBeVisible();
  expect(screen.getByText('评测条件尚未对齐')).toBeVisible();
  fireEvent.click(screen.getByRole('tab', { name: /计划内论文/ }));
  fireEvent.click(screen.getByText('Toolformer'));
  expect(screen.getByText('Original abstract')).toBeVisible();
  expect(screen.getByRole('button', { name: /版本历史/ })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '展示源码' }));
  expect(await screen.findByRole('textbox', { name: '源码' })).toHaveValue(JSON.stringify(data, null, 2));
});
it('defaults screening to papers and exposes remaining papers, notes, gaps and the plan', async () => {
  const many = [...papers, ...Array.from({ length: 7 }, (_, i) => ({ ...papers[1], paper: { ...papers[1].paper, paper_id: `extra-${i}`, title: `Paper ${i}` } }))];
  show(slot('candidate_set', { ...input, papers: many, plan: ['后续检索'], decisions: ['按方向筛选'], gaps: ['缺少对照'] }));
  expect(await screen.findByText('Toolformer')).toBeVisible();
  expect(screen.queryByText('按方向筛选')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: /展开其余 3 篇/ }));
  fireEvent.click(screen.getByText('Paper 6'));
  expect(screen.getByText('暂无摘要。')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: '返回' }));
  fireEvent.click(screen.getByRole('tab', { name: /筛选说明/ }));
  expect(screen.getByText('按方向筛选')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: /后续计划/ }));
  expect(screen.getByText('后续检索')).toBeVisible();
  fireEvent.click(screen.getByRole('tab', { name: /覆盖缺口/ }));
  expect(screen.getByText('缺少对照')).toBeVisible();
  expect(screen.queryByText('后续检索')).not.toBeInTheDocument();
});
it('removes verification chrome and empty boilerplate, retaining substantive caveats and reading info', async () => {
  show(slot('relation_analysis', { input: { ...input, papers: [{ ...papers[0], seed_relation: '该项具体结论尚未获得充分正文支持，需进一步核验。', key_contribution: '仅在单跳任务中验证，不能推到多步任务' }, papers[1]] },
    claims: [{ paper_id: 'paper-a', field: 'key_contribution', status: 'uncertain', reason: '缺少实验条件' }],
    reading: { 'paper-a': { read_level: 'related_sections', positions: [0, 2], total_blocks: 30 } } }));
  fireEvent.click(await screen.findByText('Toolformer'));
  expect(screen.getByText(/已读取相关正文片段/)).toBeVisible();
  expect(screen.getByText(/2 \/ 30 个正文块/)).toBeVisible();
  expect(screen.getByText('仅在单跳任务中验证，不能推到多步任务')).toBeVisible();
  expect(screen.queryByText('该项具体结论尚未获得充分正文支持，需进一步核验。')).not.toBeInTheDocument();
  expect(screen.queryByText('尚待核验')).not.toBeInTheDocument();
  expect(screen.queryByText('核验详情')).not.toBeInTheDocument();
});
it('uses the selected report and links its stable paper nodes to analysis', async () => {
  const relation = slot('relation_analysis', { input: { ...input, papers: [{ ...papers[0], key_contribution: '真实贡献' }, papers[1]] } });
  const report = slot('expansion_report', { narrative: '## 论文关系图\n\n```mermaid\nflowchart LR\np_paper-a("Toolformer") --- p_paper-b("ToolLLM")\n```' });
  show(relation, [relation, { ...report, selected: false, artifact_value: { data: { narrative: '旧版本' } } }, report]);
  fireEvent.click(await screen.findByRole('button', { name: '查看论文：Toolformer' }));
  expect(screen.getByText('真实贡献')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: '返回' }));
  expect(screen.getByLabelText('关系图')).toBeVisible();
});
it('supports reports without graphs and preserves exact reading order from offloaded results', async () => {
  const resultSlot = { ...slot('expansion_result', input), artifact_value: { type: 'json', url: '/papers.json' } };
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ data: input }))));
  const report = slot('expansion_report', { narrative: '## 研究问题\n\n已有研究正文。', reading_order: ['paper-b', 'paper-a'], limitations: ['只读取部分正文'] });
  show(report, [report, resultSlot]);
  expect(await screen.findByText('ToolLLM')).toBeVisible();
  const items = within(screen.getByRole('tabpanel')).getAllByRole('listitem');
  expect(items[0]).toHaveTextContent('ToolLLM'); expect(items[1]).toHaveTextContent('Toolformer');
  fireEvent.click(screen.getByRole('tab', { name: '研究报告' }));
  expect(await screen.findByRole('heading', { name: '研究问题' })).toBeVisible();
  fireEvent.click(screen.getByRole('tab', { name: /研究局限/ }));
  expect(screen.getByText('只读取部分正文')).toBeVisible();
});
it('keeps report usable when paper loading fails and retries metadata', async () => {
  vi.stubGlobal('fetch', vi.fn().mockRejectedValueOnce(new Error('unavailable')).mockResolvedValueOnce(new Response(JSON.stringify(input))));
  const resultSlot = { ...slot('expansion_result', input), artifact_value: { type: 'json', url: '/retry.json' } };
  const report = slot('expansion_report', { narrative: '保留研究正文', reading_order: ['paper-a'] });
  show(report, [report, resultSlot]);
  fireEvent.click(await screen.findByRole('tab', { name: '研究报告' }));
  expect(await screen.findByText('保留研究正文')).toBeVisible();
  fireEvent.click(await screen.findByRole('button', { name: '重新加载论文题录' }));
  fireEvent.click(screen.getByRole('tab', { name: /阅读导图/ }));
  expect(await screen.findByText('Toolformer')).toBeVisible();
});
it('merges the overview and reading route into one default map while retaining report text and the ordered list', async () => {
  const result = slot('expansion_result', { ...input, papers: [{ ...papers[0], key_contribution: '阅读导图节点对应的分析' }, papers[1]] });
  const report = slot('expansion_report', { narrative: '## 研究脉络图\n\n```mermaid\nflowchart TD\nq --- old_route\n```\n\n## 阅读导图\n\n```mermaid\nflowchart LR\nroute_base --- p_paper-a("1 Toolformer")\nroute_topic --- p_paper-b("2 ToolLLM")\n```\n\n## 研究报告\n\n研究脉络和方法比较。\n\n### 阅读建议\n先读 Toolformer，再读 ToolLLM。',
    reading_order: ['paper-a', 'paper-b'] });
  show(report, [report, result]);
  expect(await screen.findByRole('tab', { name: /阅读导图/ })).toHaveAttribute('aria-selected', 'true');
  expect(screen.queryByRole('tab', { name: /研究脉络|阅读路线/ })).not.toBeInTheDocument();
  expect(screen.getByRole('radio', { name: '阅读导图' })).toBeChecked();
  expect(screen.getByLabelText('关系图')).toBeVisible();
  expect(screen.queryByRole('list')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('tab', { name: '研究报告' }));
  expect(await screen.findByText('研究脉络和方法比较。')).toBeVisible();
  expect(screen.getByText('先读 Toolformer，再读 ToolLLM。')).toBeVisible();
  expect(screen.queryByLabelText('关系图')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('tab', { name: /阅读导图/ }));
  fireEvent.click(screen.getByRole('button', { name: '查看论文：Toolformer' }));
  expect(screen.getByText('阅读导图节点对应的分析')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: '返回' }));
  expect(screen.getByLabelText('关系图')).toBeVisible();
  fireEvent.click(screen.getByRole('radio', { name: '顺序列表' }));
  const items = within(screen.getByRole('tabpanel')).getAllByRole('listitem');
  expect(items[0]).toHaveTextContent('Toolformer'); expect(items[1]).toHaveTextContent('ToolLLM');
  expect(screen.queryByLabelText('关系图')).not.toBeInTheDocument();
});
it('keeps unrelated workflows and unknown payloads on the original renderer', async () => {
  const shown = show(slot('expansion_plan', input), [], 'unrelated-workflow');
  expect(screen.getByText(/"user_instruction"/)).toBeVisible(); shown.unmount();
  show(slot('relation_analysis', { input: null, unexpected: '保留原数据' }));
  expect(await screen.findByText(/"unexpected"/)).toBeVisible();
  expect(screen.queryByRole('tab', { name: /逐篇分析/ })).not.toBeInTheDocument();
});

it('keeps the current view when a live session refresh recreates an unchanged artifact', async () => {
  const data = { ...input, decisions: ['实时刷新后仍应显示的筛选说明'] };
  const revision = slot('candidate_set', data);
  const view = (current: SlotRevision) => <SlotRenderer slot={current} slotId='candidate_set' sessionId='paper-session'
    workflowSession={{ session_id: 'paper-session', conversation_id: 'paper-chat', workflow_id: 'paper_expansion',
      workflow_mode: 'auto', status: 'active', current_step_id: 'analyze_relations', created_at: '', updated_at: '', slots: [current] }}
    widget={{ widgetType: 'json-block', readOnly: true }} revisionCount={1} />;
  const shown = render(view(revision));
  fireEvent.click(await screen.findByRole('tab', { name: /筛选说明/ }));
  shown.rerender(view({ ...revision, artifact_value: JSON.parse(JSON.stringify(revision.artifact_value)) }));
  await waitFor(() => expect(screen.getByText('实时刷新后仍应显示的筛选说明')).toBeVisible());
  expect(screen.getByRole('tab', { name: /筛选说明/ })).toHaveAttribute('aria-selected', 'true');
});

it('labels the abstract fallback when verification leaves no substantive paper analysis', async () => {
  show(slot('relation_analysis', { input }));
  fireEvent.click(await screen.findByText('Toolformer'));
  expect(screen.getByRole('heading', { name: '论文摘要' })).toBeVisible();
  expect(screen.getByText('Original abstract')).toBeVisible();
  expect(screen.queryByRole('heading', { name: '关键贡献' })).not.toBeInTheDocument();
});
