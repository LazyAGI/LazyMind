import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ConfigProvider } from 'antd';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import zhCN from '@/i18n/locales/zh-CN';
import { getExpansion, getReviewRecordContext, type Expansion, type ReviewRecord } from '../../api/paperWorkflows';
import ResearchRecords from './ResearchRecords';
import ReviewRecordDrawer from './ReviewRecordDrawer';

vi.mock('../../api/paperWorkflows', () => ({ getExpansion: vi.fn(), getReviewRecordContext: vi.fn() }));
vi.mock('@/modules/chat/store/conversationRunning', () => ({ useConversationRunningStore: Object.assign(() => undefined, { getState: () => ({ watch: vi.fn(), unwatch: vi.fn() }) }) }));
const i18n = createInstance();
void i18n.init({ lng: 'zh-CN', resources: { 'zh-CN': { translation: zhCN } }, initImmediate: false });
const papers = ['A', 'B', 'C'].map(id => ({ paper: { paper_id: id, title: id, abstract: null }, is_seed: false, is_important: false }));
const expansion: Expansion = { id: 'source', dataset_id: 'd', conversation_id: 'expansion-chat', title: '工具学习扩展', goal: 'expansion', status: 'completed', version: 1, preparation_revision: 1, budget_mode: 'balanced', created_at: '2026-10-28T08:00:00Z', updated_at: '2026-10-28T08:00:00Z', result: { user_instruction: '', papers, candidate_directions: [], selected_directions: [] } };
const review: ReviewRecord = { id: 'review', conversation_id: 'review-chat', expansion_id: 'source', version: 1, title: '工具学习综述', paper_count: 2, source_title: expansion.title, source_available: true, created_at: '2026-10-29T08:00:00Z', updated_at: '2026-10-29T08:00:00Z' };
const direct = { ...review, id: 'direct', conversation_id: 'direct-chat', title: '直接综述', expansion_id: '', source_title: '', source_available: false };
function mount(ui: React.ReactNode) {
    return render(<I18nextProvider i18n={i18n}><ConfigProvider theme={{ token: { motion: false } }}>{ui}</ConfigProvider></I18nextProvider>);
}
beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(getReviewRecordContext).mockResolvedValue({ kind: 'review', expansion_id: 'source', source_available: true, input: { ...expansion.result, user_instruction: '综述主题：工具学习\n\n补充要求：比较方法', papers: papers.slice(0, 2) } });
    vi.mocked(getExpansion).mockResolvedValue({ record: expansion, rounds: [], chat_status: 'available', paper_importance: {}, handoffs: [], contents: papers.map(item => ({ id: item.paper.paper_id, paper_id: item.paper.paper_id, status: 'ready', content_hash: item.paper.paper_id, quality: { eligible: true, downloaded: true, parsed: true, identity: 'matched', text: 'usable', material: 'main' } })) });
});
afterEach(cleanup);

it('shows independent reviews with expansions and filters without changing the review count', () => {
    const onReview = vi.fn(), onExpansion = vi.fn();
    mount(<ResearchRecords expansions={[expansion]} reviews={[review, direct]} open loading={false} error={false} statusLabel={() => '已完成'} onExpansion={onExpansion} onReview={onReview} />);
    const rows = screen.getAllByRole('listitem');
    expect(rows[0]).toHaveTextContent('综述');
    expect(screen.getByText('来自论文库 · 未扩展')).toBeVisible();
    fireEvent.click(screen.getByRole('tab', { name: '论文综述 2' }));
    expect(screen.queryByRole('button', { name: '查看扩展成果: 工具学习扩展' })).not.toBeInTheDocument();
    expect(screen.getAllByText('本次纳入 2 篇')).toHaveLength(2);
    fireEvent.click(screen.getByRole('button', { name: '查看综述记录: 工具学习综述' }));
    expect(onReview).toHaveBeenCalledWith(review);
    fireEvent.click(screen.getByRole('tab', { name: '论文扩展 1' }));
    expect(screen.queryByText('直接综述')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /工具学习扩展/ }));
    expect(onExpansion).toHaveBeenCalledWith('source');
});

it('keeps the frozen list separate from current source counts and opens its source or conversation', async () => {
    const onSource = vi.fn(), onChat = vi.fn();
    mount(<ReviewRecordDrawer record={review} onClose={vi.fn()} onSource={onSource} onChat={onChat} />);
    await screen.findByText('本次纳入 2 篇');
    await screen.findByText('扩展结果 3 篇 · 扩展当前全文可用 3 篇');
    fireEvent.click(screen.getByText('查看本次论文清单'));
    const list = screen.getByRole('list');
    expect(within(list).getAllByRole('listitem')).toHaveLength(2);
    expect(within(list).queryByText(/C/)).not.toBeInTheDocument();
    expect(screen.getByText(/补充要求：比较方法/)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: '查看来源扩展' }));
    expect(onSource).toHaveBeenCalledWith('source');
    fireEvent.click(screen.getByRole('button', { name: '打开会话' }));
    expect(onChat).toHaveBeenCalledWith('review-chat');
});

it('retries unavailable review input and retains the frozen list when its source was deleted', async () => {
    vi.mocked(getReviewRecordContext).mockRejectedValueOnce(new Error('offline')).mockResolvedValue({ kind: 'review', expansion_id: 'source', source_available: false, input: { ...expansion.result, papers: papers.slice(0, 2) } });
    mount(<ReviewRecordDrawer record={review} onClose={vi.fn()} onSource={vi.fn()} onChat={vi.fn()} />);
    await screen.findByText('本次综述清单加载失败，请重试。');
    fireEvent.click(screen.getByRole('button', { name: /重\s*试/ }));
    await screen.findByText('本次纳入 2 篇');
    await waitFor(() => expect(screen.queryByText('本次综述清单加载失败，请重试。')).not.toBeInTheDocument());
    expect(screen.getByText('来源扩展已删除或不可访问')).toBeVisible();
    expect(screen.queryByRole('button', { name: '查看来源扩展' })).not.toBeInTheDocument();
    expect(getExpansion).not.toHaveBeenCalled();
});

it('does not label intermediate papers as expansion results in history', () => {
    mount(<ResearchRecords expansions={[{ ...expansion, status: 'running' }, { ...expansion, id: 'completed', title: '已完成的扩展' }]} reviews={[]} open loading={false} error={false} statusLabel={status => status === 'completed' ? '已完成' : '进行中'} onExpansion={vi.fn()} onReview={vi.fn()} />);
    expect(screen.getByRole('button', { name: '查看扩展成果: 工具学习扩展' })).not.toHaveTextContent('扩展结果');
    expect(screen.getByRole('button', { name: '查看扩展成果: 已完成的扩展' })).toHaveTextContent('扩展结果 3 篇');
});
