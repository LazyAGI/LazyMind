import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import PreparationPanel from './PreparationPanel';
import { confirmExpansion, submitPreparation, type ExpansionDetail } from '../../api/paperWorkflows';

vi.mock('../../api/paperWorkflows', () => ({ confirmExpansion: vi.fn(), submitPreparation: vi.fn() }));
const detail: ExpansionDetail = {
  record: { id: 'exp', dataset_id: 'papers', conversation_id: '', preparation_revision: 3, budget_mode: 'balanced',
    title: 'RAG', goal: 'expansion', status: 'awaiting_confirmation', version: 0, created_at: '', updated_at: '',
    result: { user_instruction: 'RAG', candidate_directions: [], selected_directions: [], papers: [] },
    preparation: { user_instruction: 'RAG', summary: '已找到真实论文', selected_directions: ['纠错检索'], questions: [],
      directions: [{ name: '纠错检索', question: '如何纠正检索结果', reason: '评估与纠错路线', difference: '独立探索', paper_ids: ['p'] }, { name: '检索评估', question: '如何评估检索质量', reason: '量化评估', difference: '评估路线', paper_ids: ['q'] }] } },
  rounds: [], contents: [], handoffs: [], paper_importance: {}, chat_status: 'uncreated',
};
beforeEach(() => vi.clearAllMocks());

it.each(['awaiting_confirmation', 'running', 'completed'])('keeps direction references and evidence available when %s', status => {
  const direction = { ...detail.record.preparation!.directions[0], paper_ids: ['lora', 'dora', 'adalora'], papers: [
    { paper_id: 'lora', title: 'LoRA', abstract: null, paper_url: 'https://arxiv.org/abs/2106.09685v2' },
    { paper_id: 'dora', title: 'DoRA', abstract: null, arxiv_id: '2402.09353' },
    { paper_id: 'adalora', title: 'AdaLoRA', abstract: null, doi: '10.48550/arxiv.2303.10512' },
  ] };
  const prepared = { ...detail, record: { ...detail.record, status, preparation: { ...detail.record.preparation!, directions: [direction] } } };
  render(<PreparationPanel detail={prepared} onRefresh={vi.fn()} onStart={vi.fn()} />);
  if (status === 'completed') fireEvent.click(screen.getByText('初步调研与方向确认'));
  expect(screen.getByText('3 篇参考论文')).toBeVisible();
  expect(screen.getByText('LoRA')).not.toBeVisible();
  fireEvent.click(screen.getByText('查看依据'));
  expect(screen.getByText(direction.reason)).toBeVisible();
  expect(screen.getByText(direction.difference)).toBeVisible();
  expect(screen.getByRole('link', { name: 'LoRA' })).toHaveAttribute('href', 'https://arxiv.org/abs/2106.09685v2');
  expect(screen.getByRole('link', { name: 'DoRA' })).toHaveAttribute('href', 'https://arxiv.org/abs/2402.09353');
  expect(screen.getByRole('link', { name: 'AdaLoRA' })).toHaveAttribute('href', 'https://doi.org/10.48550/arxiv.2303.10512');
  expect(screen.getByRole('link', { name: 'LoRA' })).toHaveAttribute('target', '_blank');
  fireEvent.click(screen.getByText('查看依据'));
  expect(screen.getByText('LoRA')).not.toBeVisible();
  expect(confirmExpansion).not.toHaveBeenCalled();
  expect(submitPreparation).not.toHaveBeenCalled();
});

it('uses a safe identifier link when a source is unsafe and leaves unlinked papers readable', () => {
  const direction = { ...detail.record.preparation!.directions[0], paper_ids: ['p', 'q'], papers: [
    { paper_id: 'p', title: 'Safe fallback', abstract: null, paper_url: 'javascript:alert(1)', arxiv_id: '2402.09353' },
    { paper_id: 'q', title: 'No safe source', abstract: null, paper_url: 'https://user:password@example.com/paper' },
  ] };
  const running = { ...detail, record: { ...detail.record, status: 'running', preparation: { ...detail.record.preparation!, directions: [direction] } } };
  render(<PreparationPanel detail={running} onRefresh={vi.fn()} onStart={vi.fn()} />);
  fireEvent.click(screen.getByText('查看依据'));
  const research = screen.getByRole('region', { name: '本次研究方向' });
  expect(within(research).getByRole('link', { name: 'Safe fallback' })).toHaveAttribute('href', 'https://arxiv.org/abs/2402.09353');
  expect(within(research).getByText('No safe source')).toBeVisible();
  expect(within(research).queryByRole('link', { name: 'No safe source' })).not.toBeInTheDocument();
});

it('waits for an explicit confirmation and preserves its key after an unknown response', async () => {
  vi.mocked(confirmExpansion).mockRejectedValueOnce(new Error('transport interrupted')).mockResolvedValueOnce({ expansion_id: 'exp', conversation_id: 'chat' });
  const start = vi.fn();
  render(<PreparationPanel detail={detail} onRefresh={vi.fn()} onStart={start} />);
  expect(confirmExpansion).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: '确认方向并开始拓展' }));
  expect(await screen.findByText('transport interrupted')).toBeVisible();
  const key = vi.mocked(confirmExpansion).mock.calls[0][1].request_key;
  await waitFor(() => expect(screen.getByRole('button', { name: '确认方向并开始拓展' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: '确认方向并开始拓展' }));
  await waitFor(() => expect(start).toHaveBeenCalledWith('chat'));
  expect(vi.mocked(confirmExpansion).mock.calls[1][1]).toEqual({ revision: 3, request_key: key, selected_directions: ['纠错检索'] });
});

it('requires supplementary research before starting a materially changed direction', async () => {
  vi.mocked(submitPreparation).mockResolvedValue({ expansion_id: 'exp', revision: 4, status: 'preparing' });
  render(<PreparationPanel detail={detail} onRefresh={vi.fn().mockResolvedValue(undefined)} onStart={vi.fn()} />);
  fireEvent.click(screen.getByText('调整研究范围'));
  fireEvent.change(screen.getByLabelText('调整研究范围'), { target: { value: '改为评估长上下文方法' } });
  expect(screen.getByRole('button', { name: '确认方向并开始拓展' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: '更新范围并补充调研' }));
  await waitFor(() => expect(submitPreparation).toHaveBeenCalledWith('exp', expect.objectContaining({ action: 'revise', revision: 3, user_instruction: '改为评估长上下文方法' })));
  expect(confirmExpansion).not.toHaveBeenCalled();
});

it('submits a selected answer with optional notes and locks it until the next preparation state', async () => {
  const pending = { ...detail, record: { ...detail.record, status: 'awaiting_info', preparation: { ...detail.record.preparation!, questions: [{ id: 'scope', question: '关注什么场景？', options: ['企业知识库'] }] } } };
  let accept!: (value: Awaited<ReturnType<typeof submitPreparation>>) => void;
  vi.mocked(submitPreparation).mockReturnValue(new Promise(resolve => { accept = resolve; }));
  const refresh = vi.fn().mockResolvedValue(undefined);
  const view = render(<PreparationPanel detail={pending} onRefresh={refresh} onStart={vi.fn()} />);
  expect(screen.getByRole('button', { name: '继续调研' })).toBeDisabled();
  expect(screen.getByLabelText('关注什么场景？（补充说明）')).not.toBeVisible();
  fireEvent.click(screen.getByRole('radio', { name: '企业知识库' }));
  fireEvent.click(screen.getByText('补充说明（选填）'));
  fireEvent.change(screen.getByRole('textbox', { name: '关注什么场景？（补充说明）' }), { target: { value: '仅关注内部文档' } });
  fireEvent.click(screen.getByRole('button', { name: '继续调研' }));
  await waitFor(() => expect(submitPreparation).toHaveBeenCalledWith('exp', expect.objectContaining({ action: 'answer', answers: { scope: '企业知识库\n补充说明：仅关注内部文档' }, revision: 3 })));
  expect(screen.getByRole('radio', { name: '企业知识库' })).toBeDisabled();
  expect(screen.getByRole('textbox', { name: '关注什么场景？（补充说明）' })).toBeDisabled();
  accept({ expansion_id: 'exp', revision: 4, status: 'preparing' });
  await waitFor(() => expect(refresh).toHaveBeenCalledOnce());
  expect(screen.getByRole('button', { name: '已提交' })).toBeDisabled();
  expect(screen.getByRole('radio', { name: '企业知识库' })).toBeDisabled();
  view.rerender(<PreparationPanel detail={{ ...pending, record: { ...pending.record, status: 'preparing', preparation_revision: 4 } }} onRefresh={refresh} onStart={vi.fn()} />);
  expect(screen.queryByRole('radio')).not.toBeInTheDocument();
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  expect(confirmExpansion).not.toHaveBeenCalled();
});

it('requires every answer and submits custom and open-ended responses without an earlier option or note', async () => {
  const pending = { ...detail, record: { ...detail.record, status: 'awaiting_info', preparation: { ...detail.record.preparation!, questions: [
    { id: 'scope', question: '关注什么场景？', options: ['企业知识库'] }, { id: 'limit', question: '有哪些限制？' },
  ] } } };
  vi.mocked(submitPreparation).mockResolvedValue({ expansion_id: 'exp', revision: 4, status: 'preparing' });
  render(<PreparationPanel detail={pending} onRefresh={vi.fn().mockResolvedValue(undefined)} onStart={vi.fn()} />);
  fireEvent.click(screen.getByRole('radio', { name: '企业知识库' }));
  fireEvent.click(screen.getByText('补充说明（选填）'));
  fireEvent.change(screen.getByRole('textbox', { name: '关注什么场景？（补充说明）' }), { target: { value: '旧选项的说明' } });
  fireEvent.click(screen.getByRole('radio', { name: '自行填写回答' }));
  expect(screen.queryByText('补充说明（选填）')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: '继续调研' })).toBeDisabled();
  fireEvent.change(screen.getByRole('textbox', { name: '关注什么场景？（自行填写）' }), { target: { value: '医疗文献检索' } });
  expect(screen.getByRole('button', { name: '继续调研' })).toBeDisabled();
  fireEvent.change(screen.getByRole('textbox', { name: '有哪些限制？' }), { target: { value: '仅使用公开论文' } });
  fireEvent.click(screen.getByRole('button', { name: '继续调研' }));
  await waitFor(() => expect(submitPreparation).toHaveBeenCalledWith('exp', expect.objectContaining({ answers: { scope: '医疗文献检索', limit: '仅使用公开论文' } })));
});

it('keeps failed answers editable and retries with the same key, then only refreshes after acceptance', async () => {
  const pending = { ...detail, record: { ...detail.record, status: 'awaiting_info', preparation: { ...detail.record.preparation!, questions: [{ id: 'scope', question: '关注什么场景？', options: ['企业知识库'] }] } } };
  vi.mocked(submitPreparation).mockRejectedValueOnce(new Error('submission unavailable')).mockResolvedValue({ expansion_id: 'exp', revision: 4, status: 'preparing' });
  const refresh = vi.fn().mockRejectedValueOnce(new Error('status unavailable')).mockResolvedValue(undefined);
  render(<PreparationPanel detail={pending} onRefresh={refresh} onStart={vi.fn()} />);
  fireEvent.click(screen.getByRole('radio', { name: '企业知识库' }));
  fireEvent.click(screen.getByRole('button', { name: '继续调研' }));
  expect(await screen.findByText('submission unavailable')).toBeVisible();
  expect(screen.getByRole('radio', { name: '企业知识库' })).toBeChecked();
  expect(screen.getByRole('radio', { name: '企业知识库' })).toBeEnabled();
  const key = vi.mocked(submitPreparation).mock.calls[0][1].request_key;
  fireEvent.click(screen.getByRole('button', { name: '继续调研' }));
  expect(await screen.findByText('status unavailable')).toBeVisible();
  expect(vi.mocked(submitPreparation).mock.calls[1][1].request_key).toBe(key);
  expect(screen.getByRole('radio', { name: '企业知识库' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: '刷新状态' }));
  await waitFor(() => expect(refresh).toHaveBeenCalledTimes(2));
  expect(submitPreparation).toHaveBeenCalledTimes(2);
  expect(screen.getByRole('button', { name: '已提交' })).toBeDisabled();
});


it('shows the server-started single direction without a selection or confirmation', () => {
  const started = { ...detail, record: { ...detail.record, status: 'pending', conversation_id: 'chat', preparation: { ...detail.record.preparation!, auto_confirmed: true, directions: [detail.record.preparation!.directions[0]] } } };
  render(<PreparationPanel detail={started} onRefresh={vi.fn()} onStart={vi.fn()} />);
  expect(screen.getByText('仅有一个研究方向，已自动继续拓展')).toBeVisible();
  expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '确认方向并开始拓展' })).not.toBeInTheDocument();
  expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument();
  expect(screen.getByText('标准调研')).toBeVisible();
  expect(confirmExpansion).not.toHaveBeenCalled();
});

it('updates the selected count and confirms only chosen directions', async () => {
  vi.mocked(confirmExpansion).mockResolvedValue({ expansion_id: 'exp', conversation_id: 'chat' });
  render(<PreparationPanel detail={detail} onRefresh={vi.fn()} onStart={vi.fn()} />);
  fireEvent.click(screen.getByRole('checkbox', { name: '检索评估' }));
  expect(screen.getByText('已选 2 个方向')).toBeVisible();
  fireEvent.click(screen.getByRole('checkbox', { name: '纠错检索' }));
  fireEvent.click(screen.getByRole('button', { name: '确认方向并开始拓展' }));
  await waitFor(() => expect(confirmExpansion).toHaveBeenCalledWith('exp', expect.objectContaining({ selected_directions: ['检索评估'] })));
});
