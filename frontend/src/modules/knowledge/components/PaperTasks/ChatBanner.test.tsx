import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axiosInstance } from '@/components/request';
import AcademicChatBanner from './ChatBanner';

vi.mock('@/components/request', () => ({ axiosInstance: { get: vi.fn() }, BASE_URL: '' }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

const summary = { kind: 'ppt', dataset_id: 'papers', status: 'awaiting_downstream', version: 1 };
const get = vi.mocked(axiosInstance.get);

function CurrentLocation() {
    const location = useLocation();
    return <div data-testid="location">{location.pathname}</div>;
}

function Page({ conversation }: { conversation: string }) {
    return <><AcademicChatBanner conversation={conversation} /><CurrentLocation /></>;
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
        expect(screen.getByText('paperTasks.task.ppt')).toBeInTheDocument();
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
        expect(screen.getByText('paperTasks.task.reflection')).toBeInTheDocument();
        expect(screen.queryByText('paperTasks.task.ppt')).not.toBeInTheDocument();
        view.unmount();
        fireEvent(window, new Event('focus'));
        await tick(6000);
        expect(get).toHaveBeenCalledTimes(2);
    });

    it('keeps waiting for automatic review after expansion completes and then navigates', async () => {
        const completed = { ...summary, kind: 'expansion', status: 'completed', ready_count: 2 };
        get.mockResolvedValueOnce({ data: completed }).mockResolvedValueOnce({
            data: { ...completed, next_conversation_id: 'review-chat' },
        });
        render(<MemoryRouter initialEntries={['/?paper_auto_review=1']}><Routes>
            <Route path="/" element={<Page conversation="expand-chat" />} />
            <Route path="*" element={<CurrentLocation />} />
        </Routes></MemoryRouter>);
        await tick();
        await tick(3000);
        expect(get).toHaveBeenCalledTimes(2);
        expect(screen.getByTestId('location').textContent).toContain('review-chat');
    });

    it('polls running expansions and stops after completion', async () => {
        get.mockResolvedValueOnce({ data: { ...summary, kind: 'expansion', status: 'running' } })
            .mockResolvedValue({ data: { ...summary, kind: 'expansion', status: 'completed' } });
        render(<MemoryRouter><Page conversation="expand-chat" /></MemoryRouter>);
        await tick();
        await tick(3000);
        await tick(9000);
        expect(get).toHaveBeenCalledTimes(2);
    });

    it('does not repeatedly request an inaccessible or ordinary Chat', async () => {
        get.mockRejectedValue({ isAxiosError: true, response: { status: 404 } });
        render(<MemoryRouter><Page conversation="ordinary-chat" /></MemoryRouter>);
        await tick();
        await tick(9000);
        expect(get).toHaveBeenCalledTimes(1);
    });
});
