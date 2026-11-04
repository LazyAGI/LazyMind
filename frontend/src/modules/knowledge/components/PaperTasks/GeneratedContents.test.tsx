import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createInstance } from 'i18next';
import type { AxiosRequestConfig } from 'axios';
import { I18nextProvider } from 'react-i18next';
import { axiosInstance } from '@/components/request';
import { useConversationRunningStore } from '@/modules/chat/store/conversationRunning';
import zhCN from '@/i18n/locales/zh-CN';
import GeneratedContents from './GeneratedContents';

vi.mock('@/components/request', () => ({ axiosInstance: { get: vi.fn() }, BASE_URL: '' }));
const i18n = createInstance();
void i18n.init({ lng: 'zh-CN', resources: { 'zh-CN': { translation: zhCN } }, initImmediate: false });
const items = [
    { id: 'first', kind: 'review', conversation_id: 'review-1', version: 1 },
    { id: 'second', kind: 'review', conversation_id: 'review-2', version: 1 },
];
beforeEach(() => {
    vi.mocked(axiosInstance.get).mockReset();
    useConversationRunningStore.setState({ entries: {}, watchers: {} });
});
afterEach(cleanup);

it('groups reviews in one collapsed section and opens each original conversation', async () => {
    vi.mocked(axiosInstance.get).mockImplementation(async (url: string, config?: AxiosRequestConfig) => ({ data: url.includes('chat-context')
        ? { input: { papers: Array(config?.params.conversation_id === 'review-2' ? 4 : 2).fill({}) } }
        : { display_name: url.endsWith('review-2') ? '量化微调综述' : '低秩适配综述', create_time: '2026-10-28T08:00:00Z' } }));
    const onOpen = vi.fn();
    render(<I18nextProvider i18n={i18n}><GeneratedContents items={items} onOpen={onOpen} /></I18nextProvider>);
    expect(screen.queryByRole('button', { name: '打开会话' })).not.toBeInTheDocument();
    expect(axiosInstance.get).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText('论文综述'));
    await screen.findByText('量化微调综述');
    await screen.findByText('低秩适配综述');
    const rows = screen.getAllByRole('listitem').filter((row: HTMLElement) => row.parentElement?.classList.contains('ant-list-items'));
    expect(rows[0]).toHaveTextContent('量化微调综述');
    expect(rows[0]).toHaveTextContent('本次纳入 4 篇');
    expect(rows[1]).toHaveTextContent('低秩适配综述');
    expect(screen.queryByText('已完成')).not.toBeInTheDocument();
    act(() => useConversationRunningStore.setState({ entries: {
        'review-1': { status: 'idle', terminalStatus: 'completed', confirmedAt: Date.now() },
        'review-2': { status: 'running', confirmedAt: Date.now() },
    } }));
    expect(rows[0]).toHaveTextContent(zhCN.chat.conversationRunning);
    expect(rows[1]).toHaveTextContent(zhCN.chat.conversationCompleted);
    fireEvent.click(within(rows[0]).getByRole('button', { name: '打开会话' }));
    expect(onOpen).toHaveBeenCalledWith('review-2');
    fireEvent.click(screen.getByText('论文综述'));
    await waitFor(() => expect(screen.queryByRole('button', { name: '打开会话' })).not.toBeInTheDocument());
});

it('keeps review links usable if metadata is unavailable without inventing completion', async () => {
    vi.mocked(axiosInstance.get).mockRejectedValue(new Error('unavailable'));
    render(<I18nextProvider i18n={i18n}><GeneratedContents items={items} onOpen={vi.fn()} /></I18nextProvider>);
    fireEvent.click(screen.getByText('论文综述'));
    await screen.findByText('论文综述 2');
    expect(screen.getAllByRole('button', { name: '打开会话' })).toHaveLength(2);
    expect(screen.queryByText(zhCN.chat.conversationCompleted)).not.toBeInTheDocument();
});
