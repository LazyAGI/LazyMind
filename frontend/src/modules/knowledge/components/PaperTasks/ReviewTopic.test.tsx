import { useState } from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ConfigProvider, Modal } from 'antd';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import zhCN from '@/i18n/locales/zh-CN';
import ReviewTopic from './ReviewTopic';
import { generateReviewTopic } from '../../api/paperWorkflows';

vi.mock('../../api/paperWorkflows', () => ({ generateReviewTopic: vi.fn() }));
const i18n = createInstance();
void i18n.init({ lng: 'zh-CN', resources: { 'zh-CN': { translation: zhCN } }, initImmediate: false });
function mount(initial = '') {
    function Form() {
        const [value, setValue] = useState(initial);
        const [busy, setBusy] = useState(false);
        return <Modal open footer={null}><ReviewTopic value={value} onChange={setValue} onBusyChange={setBusy}
            input={{ dataset_id: 'papers', document_ids: ['a', 'b'], user_instruction: '比较训练方法' }} /><button disabled={busy}>提交综述</button></Modal>;
    }
    render(<I18nextProvider i18n={i18n}><ConfigProvider theme={{ token: { motion: false } }}><Form /></ConfigProvider></I18nextProvider>);
}
beforeEach(() => vi.resetAllMocks());
afterEach(cleanup);

it('fills the topic directly and keeps the AI button label', async () => {
    let resolve!: (topic: string) => void;
    vi.mocked(generateReviewTopic).mockReturnValue(new Promise(done => { resolve = done; }));
    mount();
    fireEvent.click(screen.getByRole('button', { name: /AI 拟题/ }));
    expect(screen.getByRole('textbox')).toHaveValue('');
    expect(screen.getByRole('button', { name: '提交综述' })).toBeDisabled();
    resolve('工具学习方法比较');
    await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue('工具学习方法比较'));
    expect(screen.getByRole('button', { name: /AI 拟题/ })).toBeEnabled();
    expect(generateReviewTopic).toHaveBeenCalledWith(expect.objectContaining({ document_ids: ['a', 'b'], user_instruction: '比较训练方法' }), expect.any(AbortSignal));
});

it('asks before clearing, preserves on cancel, and restores the topic after failure', async () => {
    vi.mocked(generateReviewTopic).mockRejectedValue(new Error('unavailable'));
    mount('我的原主题');
    fireEvent.click(screen.getByRole('button', { name: /AI 拟题/ }));
    fireEvent.click(await screen.findByRole('button', { name: /取\s*消/ }));
    await waitFor(() => expect(screen.queryByText('替换综述主题？')).not.toBeInTheDocument());
    expect(screen.getByRole('textbox')).toHaveValue('我的原主题');
    expect(generateReviewTopic).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: /AI 拟题/ }));
    fireEvent.click(await screen.findByRole('button', { name: '清空并拟题' }));
    await screen.findByText('AI 拟题失败，已保留原内容，请重试。');
    expect(screen.getByRole('textbox')).toHaveValue('我的原主题');
    expect(screen.getByRole('button', { name: '提交综述' })).toBeEnabled();
});
