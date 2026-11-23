import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import ReadingPreferencesModal from './ReadingPreferencesModal';
import { presetModes } from '../api/readingPreparation';
const api = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn() }));
vi.mock('../api/readingPreparation', async importOriginal => ({ ...await importOriginal<typeof import('../api/readingPreparation')>(), getReadingPreferences: api.get, saveReadingPreferences: api.save }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: {}, getLocalizedErrorMessage: (e: Error) => e.message }));
afterEach(cleanup);
async function chooseMode(category: string, mode: string) {
  fireEvent.mouseDown(screen.getByRole('combobox', { name: category }));
  fireEvent.click(await screen.findByText(mode, { selector: '.ant-select-item-option-content' }));
}
beforeEach(() => { api.get.mockReset().mockResolvedValue({ preset: 'demand', modes: presetModes('demand'), limits: { daily_pdf_documents: 100, daily_study_documents: 50, automatic_concurrency: 1 }, capabilities: [{ id: 'pdf_parse', available: false, unavailable_reason: '请配置可用的 MinerU 解析服务' }], usage: { pdf_parse: { used: 9, limit: 100 }, ai_study: { used: 50, limit: 50 } } }); api.save.mockReset(); });
it('loads personal defaults, explains missing configuration, and preserves edits after failed save', async () => {
  const close = vi.fn(); api.save.mockRejectedValueOnce(new Error('保存失败，请重试'));
  render(<ReadingPreferencesModal open onClose={close} />);
  await screen.findByText('请配置可用的 MinerU 解析服务');
  await chooseMode('PDF 解析', '自动');
  const save = screen.getByRole('button', { name: '保存设置' });
  fireEvent.click(save);
  await screen.findByText('保存失败，请重试');
  expect(close).not.toHaveBeenCalled();
  expect(api.save).toHaveBeenLastCalledWith(expect.objectContaining({ preset: 'custom', modes: { paper_guide: 'on_demand', pdf_parse: 'automatic', ai_study: 'on_demand' } }));
  api.save.mockResolvedValueOnce({});
  await waitFor(() => expect(save).not.toHaveClass('ant-btn-loading'));
  fireEvent.click(save);
  await waitFor(() => expect(close).toHaveBeenCalledOnce());
  expect(api.save).toHaveBeenLastCalledWith(expect.objectContaining({ preset: 'custom', modes: { paper_guide: 'on_demand', pdf_parse: 'automatic', ai_study: 'on_demand' } }));
});
it('allows automatic study to use a retained structure when parse mode is on demand', async () => {
  api.get.mockResolvedValueOnce({ preset: 'custom', modes: { ...presetModes('demand'), ai_study: 'automatic' }, limits: { daily_pdf_documents: 100, daily_study_documents: 50, automatic_concurrency: 1 }, capabilities: [] });
  render(<ReadingPreferencesModal open onClose={() => {}} />);
  await screen.findByText(/自动精读优先使用已有结构/);
  expect(screen.getByRole('button', { name: '保存设置' })).toBeEnabled();
});
it('keeps independent provider selections when changing a category and exposes real configuration destinations', async () => {
  api.get.mockResolvedValueOnce({ preset: 'demand', parser_ref: 'own:parser', study_model_ref: 'shared:missing', modes: presetModes('demand'), limits: { daily_pdf_documents: 100, daily_study_documents: 50, automatic_concurrency: 1 }, capabilities: [], provider_selections: { parser: { options: [{ id: 'own:parser', label: 'MinerU / local' }], default_label: 'MinerU' }, study: { options: [], unavailable_reason: '所选服务不可用，请重新选择或配置' } } });
  render(<ReadingPreferencesModal open onClose={() => {}} />);
  await screen.findByText('文档解析服务');
  expect(screen.getByRole('link', { name: '配置文档解析服务' })).toHaveAttribute('href', '/settings?section=model_defaults&service=parsing');
  expect(screen.getByRole('link', { name: '配置研读模型' })).toHaveAttribute('href', '/settings?section=models');
  expect(screen.getByText('所选服务不可用，请重新选择或配置')).toBeInTheDocument();
  await chooseMode('AI 论文精读', '禁用生成');
  fireEvent.click(screen.getByRole('button', { name: '保存设置' }));
  await waitFor(() => expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ parser_ref: 'own:parser', study_model_ref: 'shared:missing', preset: 'custom', modes: { paper_guide: 'on_demand', pdf_parse: 'on_demand', ai_study: 'disabled' } })));
});
it('returns a missing selection to inherited default without changing either category mode', async () => {
  api.get.mockResolvedValueOnce({ preset: 'demand', parser_ref: 'own:gone', study_model_ref: '', modes: presetModes('demand'), limits: { daily_pdf_documents: 100, daily_study_documents: 50, automatic_concurrency: 1 }, capabilities: [], provider_selections: { parser: { options: [], default_label: 'MinerU' }, study: { options: [], default_label: 'Chat' } } });
  render(<ReadingPreferencesModal open onClose={() => {}} />);
  const parser = await screen.findByRole('combobox', { name: '文档解析服务' });
  fireEvent.mouseDown(parser);
  fireEvent.click(await screen.findByText('跟随默认（MinerU）'));
  fireEvent.click(screen.getByRole('button', { name: '保存设置' }));
  await waitFor(() => expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ parser_ref: '', study_model_ref: '', preset: 'custom', modes: { paper_guide: 'on_demand', pdf_parse: 'on_demand', ai_study: 'on_demand' } })));
});
it('shows both saved modes without shortcut presets or a disclosure step', async () => {
  api.get.mockResolvedValueOnce({ preset: 'advance', modes: { paper_guide: 'on_demand', pdf_parse: 'automatic', ai_study: 'on_demand' }, limits: { daily_pdf_documents: 100, daily_study_documents: 50, automatic_concurrency: 1 }, capabilities: [] });
  render(<ReadingPreferencesModal open onClose={() => {}} />);
  expect(await screen.findByRole('combobox', { name: 'PDF 解析' })).toBeInTheDocument();
  expect(screen.getByRole('combobox', { name: 'AI 论文精读' })).toBeInTheDocument();
  expect(screen.queryByRole('radio')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /收起设置|逐项调整/ })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '保存设置' }));
  await waitFor(() => expect(api.save).toHaveBeenCalledWith(expect.objectContaining({
    preset: 'custom', modes: { paper_guide: 'on_demand', pdf_parse: 'automatic', ai_study: 'on_demand' },
  })));
});

it('shows independent daily usage, quota wait, and only two category controls', async () => {
  render(<ConfigProvider theme={{ token: { motion: false } }}><ReadingPreferencesModal open onClose={() => {}} /></ConfigProvider>);
  expect(await screen.findByText(/9 \/ 100 篇/)).toBeInTheDocument();
  expect(screen.getByText(/50 \/ 50 篇/)).toBeInTheDocument();
  expect(screen.getByText(/AI 论文精读额度已用尽/)).toBeInTheDocument();
  expect(screen.queryByRole('combobox', { name: /公式研读|图片研读|表格研读|文档对话准备/ })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: /每日自动额度/ }));
  fireEvent.change(screen.getByRole('spinbutton', { name: 'PDF 解析每日额度' }), { target: { value: '120' } });
  fireEvent.click(screen.getByRole('button', { name: '保存设置' }));
  await waitFor(() => expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ limits: { daily_pdf_documents: 120, daily_study_documents: 50, automatic_concurrency: 1 } })));
});
it('only offers on-demand or disabled guide generation even in the full preset', async () => {
 api.get.mockResolvedValueOnce({ preset: 'full', modes: presetModes('full'), limits: { daily_pdf_documents: 100, daily_study_documents: 50, automatic_concurrency: 1 }, capabilities: [] });
 render(<ReadingPreferencesModal open onClose={() => {}} />);
 const guide = await screen.findByRole('combobox', { name: '论文导读' });
 fireEvent.mouseDown(guide);
 expect(screen.queryByText('自动', { selector: '.ant-select-item-option-content' })).not.toBeInTheDocument();
 fireEvent.click(screen.getByText('禁用生成', { selector: '.ant-select-item-option-content' }));
 fireEvent.click(screen.getByRole('button', { name: '保存设置' }));
 await waitFor(() => expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ modes: { pdf_parse: 'automatic', ai_study: 'automatic', paper_guide: 'disabled' } })));
});
