import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axiosInstance } from '@/components/request';
import AcademicChatBanner from './ChatBanner';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import zhCN from '@/i18n/locales/zh-CN';

vi.mock('@/components/request', () => ({ axiosInstance: { get: vi.fn() }, BASE_URL: '' }));
const i18n = createInstance();
void i18n.init({ lng: 'zh-CN', resources: { 'zh-CN': { translation: zhCN } }, initImmediate: false });

const summary = { kind: 'ppt', dataset_id: 'papers', status: 'awaiting_downstream', version: 1 };
const get = vi.mocked(axiosInstance.get);

function CurrentLocation() {
    const location = useLocation();
    return <div data-testid="location">{location.pathname}</div>;
}

function Page({ conversation }: { conversation: string }) {
    return <I18nextProvider i18n={i18n}><AcademicChatBanner conversation={conversation} /><CurrentLocation /></I18nextProvider>;
}

async function tick(ms = 0) {
    await act(async () => { vi.advanceTimersByTime(ms); });
}

beforeEach(() => {
    vi.useFakeTimers();
    get.mockReset();
});
afterEach(() => {
    cleanup();
    vi.useRealTimers();
});

describe('paper Chat navigation', () => {
    it('requests only a summary for static handoffs and refreshes on focus', async () => {
        get.mockResolvedValue({ data: summary });
        render(<MemoryRouter><Page conversation="ppt-chat" /></MemoryRouter>);
        await tick();
        expect(get).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({
            params: { conversation_id: 'ppt-chat', view: 'summary' },
        }));
        await tick(9000);
        expect(get).toHaveBeenCalledTimes(1);
        fireEvent(window, new Event('focus'));
        await tick();
        expect(get).toHaveBeenCalledTimes(2);
        expect(screen.getByText('汇报 PPT')).toBeInTheDocument();
    });

    it('does not overlap slow requests or apply a previous Chat response', async () => {
        let finish!: (value: { data: typeof summary }) => void;
        get.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
        const view = render(<MemoryRouter><Page conversation="old-chat" /></MemoryRouter>);
        fireEvent(window, new Event('focus'));
        await tick(9000);
        expect(get).toHaveBeenCalledTimes(1);
        get.mockResolvedValue({ data: { ...summary, kind: 'reflection' } });
        view.rerender(<MemoryRouter><Page conversation="new-chat" /></MemoryRouter>);
        await tick();
        await act(async () => { finish({ data: summary }); });
        expect(screen.getByText('阅读心得')).toBeInTheDocument();
        expect(screen.queryByText('汇报 PPT')).not.toBeInTheDocument();
        view.unmount();
        fireEvent(window, new Event('focus'));
        await tick(6000);
        expect(get).toHaveBeenCalledTimes(2);
    });

    it('keeps waiting for automatic review after expansion completes and then navigates', async () => {
        const completed = { ...summary, kind: 'expansion', goal: 'review', status: 'completed', ready_count: 2 };
        get.mockResolvedValueOnce({ data: completed }).mockResolvedValueOnce({
            data: { ...completed, next_conversation_id: 'review-chat' },
        });
        render(<MemoryRouter initialEntries={['/?paper_auto_review=1']}><Routes>
            <Route path="/" element={<Page conversation="expand-chat" />} />
            <Route path="*" element={<CurrentLocation />} />
        </Routes></MemoryRouter>);
        await tick();
        expect(screen.getByText('文献准备已完成')).toBeVisible();
        expect(screen.queryByText('还需要补充可用全文')).not.toBeInTheDocument();
        await tick(3000);
        expect(get).toHaveBeenCalledTimes(2);
        expect(screen.getByTestId('location').textContent).toContain('review-chat');
    });
    it('restores review progress without a URL flag and preserves manual navigation', async () => {
        const completed = { ...summary, kind: 'expansion', goal: 'review', expansion_id: 'r', status: 'completed', ready_count: 1 };
        get.mockResolvedValueOnce({ data: completed }).mockResolvedValue({ data: { ...completed, next_conversation_id: 'review-chat' } });
        render(<MemoryRouter><Page conversation="reopened-chat" /></MemoryRouter>);
        await tick();
        expect(screen.getByText('论文综述 · 资料准备')).toBeVisible();
        expect(screen.getByText('还需要补充可用全文')).toBeVisible();
        const flow = within(screen.getByRole('region', { name: '论文综述流程' }));
        for (const stage of ['初步调研', '确认方向', '检索与阅读', '生成综述']) expect(flow.getByText(stage)).toBeVisible();
        await tick(3000);
        expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/);
        fireEvent.click(screen.getByRole('button', { name: '查看论文综述' }));
        expect(screen.getByTestId('location')).toHaveTextContent('review-chat');
    });

    it('shows material handoff in a review chat without claiming generation', async () => {
        get.mockResolvedValue({ data: { ...summary, kind: 'review' } });
        render(<MemoryRouter><Page conversation="review-chat" /></MemoryRouter>);
        await tick();
        expect(screen.getByRole('region', { name: '论文综述流程' })).toBeVisible();
        expect(screen.getByText('已接收本次确认的论文、主题与补充要求。')).toBeVisible();
        expect(screen.queryByText('论文综述已生成')).not.toBeInTheDocument();
    });

    it('polls running expansions and stops after completion', async () => {
        get.mockResolvedValueOnce({ data: { ...summary, kind: 'expansion', status: 'running' } })
            .mockResolvedValue({ data: { ...summary, kind: 'expansion', status: 'completed' } });
        render(<MemoryRouter><Page conversation="expand-chat" /></MemoryRouter>);
        await tick();
        await tick(3000);
        await tick(9000);
        expect(get).toHaveBeenCalledTimes(2);
        expect(screen.queryByRole('region', { name: '论文综述流程' })).not.toBeInTheDocument();
    });

    it('does not repeatedly request an inaccessible or ordinary Chat', async () => {
        get.mockRejectedValue({ isAxiosError: true, response: { status: 404 } });
        render(<MemoryRouter><Page conversation="ordinary-chat" /></MemoryRouter>);
        await tick();
        await tick(9000);
        expect(get).toHaveBeenCalledTimes(1);
    });
});
