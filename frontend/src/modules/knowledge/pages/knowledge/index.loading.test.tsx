import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {MemoryRouter, Route, Routes} from 'react-router-dom';
import {afterEach, expect, it, vi} from 'vitest';
import {axiosInstance} from '@/components/request';
import Detail from './index';
vi.mock('react-pdf', () => ({pdfjs: {GlobalWorkerOptions: {}}, Document: () => null, Page: () => null}));
vi.mock('@/modules/knowledge/components/FileViewer', () => ({default: () => null}));
vi.mock('@/modules/knowledge/components/PdfTemporaryChat', () => ({default: () => null}));
const originalAdapter = axiosInstance.defaults.adapter;
afterEach(() => {axiosInstance.defaults.adapter = originalAdapter; vi.unstubAllGlobals();});
it('does not render the legacy document layout while file metadata is unresolved', () => {
  axiosInstance.defaults.adapter = () => new Promise(() => {});
  const {container} = render(<MemoryRouter initialEntries={['/doc/library/paper']}><Routes><Route path="/doc/:knowledgeBaseId/:knowledgeId" element={<Detail/>}/></Routes></MemoryRouter>);
  expect(container.querySelector('.paper-nav-pane')).toBeNull();
  expect(screen.getByRole('status')).toBeVisible();
});

function renderDocument() {
  return render(<MemoryRouter initialEntries={['/doc/library/paper']}><Routes><Route path="/doc/:knowledgeBaseId/:knowledgeId" element={<Detail/>}/></Routes></MemoryRouter>);
}

it('switches from the loading surface directly into the PDF workspace', async () => {
  const observed: Element[] = [];
  vi.stubGlobal("ResizeObserver", class { observe(element: Element) {observed.push(element);} disconnect() {} unobserve() {} });
  let finish!: () => void;
  axiosInstance.defaults.adapter = config => new Promise(resolve => {
    if (config.url?.endsWith('/documents/paper')) finish = () => resolve({config, status: 200, statusText: 'OK', headers: {}, data: {display_name: 'paper.pdf'}});
  });
  const {container} = renderDocument();
  expect(screen.getByRole('status')).toBeVisible();
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  await act(async () => {finish();});
  await waitFor(() => expect(container.querySelector('.is-paper-document')).not.toBeNull());
  expect(container.querySelector('.paper-nav-pane')).toBeNull();
  expect(observed).toContain(container.querySelector('.knowledge-paper-workspace'));
  expect(screen.queryByRole('status')).toBeNull();
});

it('keeps non-PDF navigation available after the file type is known', async () => {
  axiosInstance.defaults.adapter = config => config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config, status: 200, statusText: 'OK', headers: {}, data: {display_name: 'notes.txt'}})
    : new Promise(() => {});
  const {container} = renderDocument();
  await waitFor(() => expect(container.querySelector('.paper-nav-pane')).not.toBeNull());
  expect(screen.queryByRole('status')).toBeNull();
});

it('shows a retryable error instead of the legacy layout if metadata fails', async () => {
  let attempt = 0;
  axiosInstance.defaults.adapter = config => {
    if (!config.url?.endsWith('/documents/paper')) return new Promise(() => {});
    if (++attempt === 1) return Promise.reject(new Error('metadata unavailable'));
    return Promise.resolve({config, status: 200, statusText: 'OK', headers: {}, data: {display_name: 'paper.pdf'}});
  };
  const {container} = renderDocument();
  const error = await screen.findByRole('alert');
  expect(container.querySelector('.paper-nav-pane')).toBeNull();
  fireEvent.click(error.querySelector('button')!);
  await waitFor(() => expect(container.querySelector('.is-paper-document')).not.toBeNull());
  expect(screen.queryByRole('alert')).toBeNull();
});
