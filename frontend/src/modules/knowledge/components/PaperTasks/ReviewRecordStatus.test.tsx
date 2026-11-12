import { Profiler } from 'react';
import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { useConversationRunningStore } from '@/modules/chat/store/conversationRunning';
import zhCN from '@/i18n/locales/zh-CN';
import ReviewRecordStatus from './ReviewRecordStatus';

type Entry = ReturnType<typeof useConversationRunningStore.getState>['entries'][string];
const i18n = createInstance();
void i18n.init({ lng: 'zh-CN', resources: { 'zh-CN': { translation: zhCN } }, initImmediate: false });

function setEntry(entry?: Omit<Entry, 'confirmedAt'>) {
    act(() => useConversationRunningStore.setState({ entries: entry ? { review: { ...entry, confirmedAt: 1 } } : {} }));
}

function mount(kind?: string) {
    return render(<I18nextProvider i18n={i18n}><ReviewRecordStatus conversationId="review" kind={kind} /></I18nextProvider>);
}

beforeEach(() => useConversationRunningStore.setState({ entries: {}, watchers: {} }));
afterEach(() => {
    cleanup();
    useConversationRunningStore.setState({ entries: {}, watchers: {} });
});

const cases: { name: string; entry?: Omit<Entry, 'confirmedAt'>; label: string }[] = [
    { name: 'a missing status', label: zhCN.chat.conversationStatusUnavailable },
    { name: 'idle without a terminal result', entry: { status: 'idle' }, label: zhCN.paperTasks.flowStatus.waiting },
    { name: 'running', entry: { status: 'running' }, label: `综述会话 · ${zhCN.chat.conversationRunning}` },
    { name: 'completed', entry: { status: 'idle', terminalStatus: 'completed' }, label: `综述会话 · ${zhCN.chat.conversationCompleted}` },
    { name: 'failed', entry: { status: 'idle', terminalStatus: 'failed' }, label: `综述会话 · ${zhCN.chat.conversationFailed}` },
    { name: 'canceled', entry: { status: 'idle', terminalStatus: 'canceled' }, label: `综述会话 · ${zhCN.chat.conversationCanceled}` },
    { name: 'unknown', entry: { status: 'unknown' }, label: `综述会话 · ${zhCN.chat.conversationStatusUnavailable}` },
];

it.each(cases)('renders $name without missing-state errors', ({ entry, label }) => {
    setEntry(entry);
    mount();
    expect(screen.getByRole('status').textContent).toBe(label);
});

it('updates waiting, confirmed terminal and unavailable labels as the same conversation changes', () => {
    mount();
    for (const { entry, label } of [cases[1], cases[2], cases[3], cases[6], cases[0]]) {
        setEntry(entry);
        expect(screen.getByRole('status').textContent).toBe(label);
    }
});

it('keeps non-review status labels unprefixed', () => {
    setEntry({ status: 'running' });
    mount('outline');
    expect(screen.getByRole('status').textContent).toBe(zhCN.chat.conversationRunning);
});

it('ignores metadata-only updates while still rendering a changed conversation status', () => {
    setEntry({ status: 'idle', terminalStatus: 'completed' });
    const onRender = vi.fn();
    render(<I18nextProvider i18n={i18n}><Profiler id="review-status" onRender={onRender}>
        <ReviewRecordStatus conversationId="review" />
    </Profiler></I18nextProvider>);
    const initialCommits = onRender.mock.calls.length;
    act(() => useConversationRunningStore.setState(state => ({ entries: {
        ...state.entries,
        review: { ...state.entries.review, confirmedAt: 2, terminalRead: true, terminalVersion: 'new-version' },
        other: { status: 'running', confirmedAt: 2 },
    } })));
    expect(onRender).toHaveBeenCalledTimes(initialCommits);
    expect(screen.getByRole('status').textContent).toBe(`综述会话 · ${zhCN.chat.conversationCompleted}`);

    setEntry({ status: 'running' });
    expect(onRender.mock.calls.length).toBeGreaterThan(initialCommits);
    expect(screen.getByRole('status').textContent).toBe(`综述会话 · ${zhCN.chat.conversationRunning}`);
});
