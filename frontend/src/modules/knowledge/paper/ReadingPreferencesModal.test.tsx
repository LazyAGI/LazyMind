import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import ReadingPreferencesModal from './ReadingPreferencesModal';
import { presetModes } from '../api/readingPreparation';
const api = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn() }));
vi.mock('../api/readingPreparation', async importOriginal => ({ ...await importOriginal<typeof import('../api/readingPreparation')>(), getReadingPreferences: api.get, saveReadingPreferences: api.save }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: {}, getLocalizedErrorMessage: (e: Error) => e.message }));
afterEach(cleanup);
beforeEach(() => { api.get.mockReset().mockResolvedValue({ preset: 'demand', modes: presetModes('demand'), limits: { daily_documents: 5, study_items_per_document: 10, automatic_concurrency: 1 }, capabilities: [{ id: 'pdf_parse', available: false, unavailable_reason: '请配置可用的 MinerU 解析服务' }] }); api.save.mockReset(); });
it('loads personal defaults, explains missing configuration, and preserves edits after failed save', async () => {
  const close = vi.fn(); api.save.mockRejectedValueOnce(new Error('保存失败，请重试'));
  render(<ReadingPreferencesModal open onClose={close} />);
  await screen.findByText('请配置可用的 MinerU 解析服务');
  fireEvent.click(screen.getByRole('radio', { name: /充分准备/ }));
  fireEvent.click(screen.getByRole('button', { name: /保存预设/ }));
  await screen.findByText('保存失败，请重试');
  expect(close).not.toHaveBeenCalled();
  expect(api.save).toHaveBeenLastCalledWith(expect.objectContaining({ preset: 'full', modes: presetModes('full') }));
  expect(screen.getByRole('radio', { name: /充分准备/ })).toBeChecked();
  api.save.mockResolvedValueOnce({});
  fireEvent.click(screen.getByRole('button', { name: /保存预设/ }));
  await waitFor(() => expect(close).toHaveBeenCalledOnce());
});
it('explains invalid automatic study dependency and blocks save before submission', async () => {
  api.get.mockResolvedValueOnce({ preset: 'custom', modes: { ...presetModes('demand'), formula: 'automatic' }, limits: { daily_documents: 5, study_items_per_document: 10, automatic_concurrency: 1 }, capabilities: [] });
  render(<ReadingPreferencesModal open onClose={() => {}} />);
  await screen.findByText('自动研读公式、图片或表格需要先将「结构解析」设为自动。');
  expect(screen.getByRole('button', { name: /保存预设/ })).toBeDisabled();
  expect(api.save).not.toHaveBeenCalled();
});
it('keeps independent provider selections across presets and exposes real configuration destinations', async () => {
  api.get.mockResolvedValueOnce({ preset: 'demand', parser_ref: 'own:parser', study_model_ref: 'shared:missing', modes: presetModes('demand'), limits: { daily_documents: 5, study_items_per_document: 10, automatic_concurrency: 1 }, capabilities: [], provider_selections: { parser: { options: [{ id: 'own:parser', label: 'MinerU / local' }], default_label: 'MinerU' }, study: { options: [], unavailable_reason: '所选服务不可用，请重新选择或配置' } } });
  render(<ReadingPreferencesModal open onClose={() => {}} />);
  await screen.findByText('文档解析服务');
  expect(screen.getByRole('link', { name: '配置文档解析服务' })).toHaveAttribute('href', '/settings?section=model_defaults&service=parsing');
  expect(screen.getByRole('link', { name: '配置研读模型' })).toHaveAttribute('href', '/settings?section=models');
  expect(screen.getByText('所选服务不可用，请重新选择或配置')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('radio', { name: /充分准备/ }));
  fireEvent.click(screen.getByRole('button', { name: /保存预设/ }));
  await waitFor(() => expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ parser_ref: 'own:parser', study_model_ref: 'shared:missing', preset: 'full' })));
});
it('returns a missing selection to inherited default without changing the preset', async () => {
  api.get.mockResolvedValueOnce({ preset: 'demand', parser_ref: 'own:gone', study_model_ref: '', modes: presetModes('demand'), limits: { daily_documents: 5, study_items_per_document: 10, automatic_concurrency: 1 }, capabilities: [], provider_selections: { parser: { options: [], default_label: 'MinerU' }, study: { options: [], default_label: 'Chat' } } });
  render(<ReadingPreferencesModal open onClose={() => {}} />);
  const parser = await screen.findByRole('combobox', { name: '文档解析服务' });
  fireEvent.mouseDown(parser);
  fireEvent.click(await screen.findByText('跟随默认（MinerU）'));
  fireEvent.click(screen.getByRole('button', { name: /保存预设/ }));
  await waitFor(() => expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ parser_ref: '', study_model_ref: '', preset: 'demand' })));
});
it('expands per-feature controls by default and keeps them open when choosing a plan', async () => {
  render(<ReadingPreferencesModal open onClose={() => {}} />);
  await screen.findByRole('radio', { name: /按需准备/ });
  expect(screen.getByRole('combobox', { name: '公式研读' })).toBeInTheDocument();
  expect(screen.queryByText('论文速览')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('radio', { name: /提前准备/ }));
  expect(screen.getByRole('combobox', { name: '公式研读' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: /保存预设/ }));
  await waitFor(() => expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ preset: 'advance', limits: { daily_documents: 5, study_items_per_document: 10, automatic_concurrency: 1 } })));
});

it('places services above plans and restores expanded controls when reopened', async () => {
 const view=render(<ReadingPreferencesModal open onClose={() => {}} />);
 const services=await screen.findByText('服务与模型');
 expect(services.compareDocumentPosition(screen.getByText('准备方案')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
 for (const name of ['结构解析','文档对话准备','公式研读','图片研读','表格研读']) expect(screen.getByRole('combobox',{name})).toBeInTheDocument();
 fireEvent.click(screen.getByRole('button',{name:/收起设置/}));
 expect(screen.queryByRole('combobox',{name:'公式研读'})).not.toBeInTheDocument();
 view.rerender(<ReadingPreferencesModal open={false} onClose={() => {}} />);
 view.rerender(<ReadingPreferencesModal open onClose={() => {}} />);
 expect(await screen.findByRole('combobox',{name:'公式研读'})).toBeInTheDocument();
});
