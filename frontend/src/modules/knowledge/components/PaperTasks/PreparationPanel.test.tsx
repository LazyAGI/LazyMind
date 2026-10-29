import { fireEvent, render, screen, waitFor } from '@testing-library/react';
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

it('saves a consequential answer and resumes the same preparation record', async () => {
  const pending = { ...detail, record: { ...detail.record, status: 'awaiting_info', preparation: { ...detail.record.preparation!, questions: [{ id: 'scope', question: '关注什么场景？', options: ['企业知识库'] }] } } };
  vi.mocked(submitPreparation).mockResolvedValue({ expansion_id: 'exp', revision: 4, status: 'preparing' });
  render(<PreparationPanel detail={pending} onRefresh={vi.fn().mockResolvedValue(undefined)} onStart={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: '企业知识库' }));
  fireEvent.click(screen.getByRole('button', { name: '提交补充并继续调研' }));
  await waitFor(() => expect(submitPreparation).toHaveBeenCalledWith('exp', expect.objectContaining({ action: 'answer', answers: { scope: '企业知识库' }, revision: 3 })));
  expect(confirmExpansion).not.toHaveBeenCalled();
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
