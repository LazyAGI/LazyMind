import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {Link, MemoryRouter, Route, Routes} from 'react-router-dom';
import {afterEach, expect, it, vi} from 'vitest';
import {axiosInstance} from '@/components/request';
import type {Dataset} from '@/api/generated/knowledge-client';
import {useDatasetPermissionStore} from '@/modules/knowledge/store/dataset_permission';
import Detail from './index';
vi.mock('react-pdf', () => ({pdfjs: {GlobalWorkerOptions: {}}, Document: () => null, Page: () => null}));
const surfaces=vi.hoisted(()=>({viewer:{} as Record<string,unknown>,chat:{} as Record<string,unknown>}));
vi.mock('@/modules/knowledge/components/FileViewer', () => ({default: (props:Record<string,unknown>) => {surfaces.viewer=props;return null;}}));
vi.mock('@/modules/knowledge/components/PdfTemporaryChat', () => ({default: (props:Record<string,unknown>) => {surfaces.chat=props;return null;}}));
const originalAdapter = axiosInstance.defaults.adapter;
afterEach(() => {axiosInstance.defaults.adapter = originalAdapter;useDatasetPermissionStore.getState().clearDataset();vi.unstubAllGlobals();});
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
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'academic_papers'} as Dataset);
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
  expect(surfaces.viewer.pdfIRTarget).toEqual(expect.objectContaining({dataset:'library',document:'paper'}));
  expect(surfaces.chat.paperMode).toBe(true);
  expect(screen.queryByRole('status')).toBeNull();
});

it('keeps an ordinary PDF in the document workspace with existing learning and chat actions',async()=>{
  surfaces.chat={};
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'general'} as Dataset);
  axiosInstance.defaults.adapter = config => config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'manual.pdf'}})
    : new Promise(()=>{});
  const {container}=renderDocument();
  await waitFor(()=>expect(container.querySelector('.paper-nav-pane')).not.toBeNull());
  expect(container.querySelector('.is-paper-document')).toBeNull();
  expect(container.querySelector('.paper-workspace-dock')).toBeNull();
  expect(screen.getByRole('tab',{name:/References|参考文献/})).toBeInTheDocument();
  expect(surfaces.viewer.pdfIRTarget).toBeUndefined();
  expect(surfaces.viewer.onImportReferenceSelection).toBeTypeOf('function');
  expect(surfaces.viewer.onPdfTranslateSelection).toBeTypeOf('function');
  expect(surfaces.viewer.onLearningSelection).toBeTypeOf('function');
  expect(container.querySelector('.paper-chat-pane.is-collapsed')).not.toBeNull();
  expect(surfaces.chat.paperMode).toBeUndefined();
  fireEvent.click(screen.getByRole('button',{name:/Open document assistant|展开文档助手/}));
  await waitFor(()=>expect(surfaces.chat.paperMode).toBe(false));
});

it('keeps non-PDF navigation available after the file type is known', async () => {
  axiosInstance.defaults.adapter = config => config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config, status: 200, statusText: 'OK', headers: {}, data: {display_name: 'notes.txt'}})
    : new Promise(() => {});
  const {container} = renderDocument();
  await waitFor(() => expect(container.querySelector('.paper-nav-pane')).not.toBeNull());
  expect(screen.queryByRole('status')).toBeNull();
});

it('does not show the previous document while the next document metadata loads', async () => {
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'general'} as Dataset);
  axiosInstance.defaults.adapter = config => config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'first.pdf'}})
    : new Promise(()=>{});
  render(<MemoryRouter initialEntries={['/doc/library/paper']}><Link to="/doc/library/next">Next document</Link><Routes><Route path="/doc/:knowledgeBaseId/:knowledgeId" element={<Detail/>}/></Routes></MemoryRouter>);
  await waitFor(()=>expect(screen.getAllByText('first.pdf').length).toBeGreaterThan(0));
  fireEvent.click(screen.getByRole('link',{name:'Next document'}));
  expect(screen.getByRole('status')).toBeVisible();
  expect(screen.queryAllByText('first.pdf')).toHaveLength(0);
});

it('shows a retryable error instead of the legacy layout if metadata fails', async () => {
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'academic_papers'} as Dataset);
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

it('does not guess the PDF workspace when the library scene cannot be loaded', async () => {
  axiosInstance.defaults.adapter = config => config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'paper.pdf'}})
    : Promise.reject(new Error('dataset unavailable'));
  const {container}=renderDocument();
  await waitFor(()=>expect(screen.getByRole('alert')).toBeVisible());
  expect(container.querySelector('.knowledge-paper-workspace')).toBeNull();
  expect(screen.getByRole('button',{name:/Retry|重\s*试/})).toBeInTheDocument();
});
