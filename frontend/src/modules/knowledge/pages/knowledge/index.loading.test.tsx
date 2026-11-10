import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {Link, MemoryRouter, Route, Routes} from 'react-router-dom';
import {afterEach, expect, it, vi} from 'vitest';
import {axiosInstance} from '@/components/request';
import type {Dataset} from '@/api/generated/knowledge-client';
import {useDatasetPermissionStore} from '@/modules/knowledge/store/dataset_permission';
import Detail from './index';
vi.mock('react-pdf', () => ({pdfjs: {GlobalWorkerOptions: {}}, Document: () => null, Page: () => null}));
const surfaces=vi.hoisted(()=>({viewer:{} as Record<string,unknown>,viewerRenders:0,chat:{} as Record<string,unknown>,vocabularyEnabled:false,vocabulary:{} as Record<string,unknown>,vocabularyModal:{} as Record<string,unknown>}));
vi.mock('@/modules/knowledge/components/FileViewer', () => ({default: (props:Record<string,unknown>) => {surfaces.viewer=props;surfaces.viewerRenders++;return <div>{typeof props.pdfHeaderEnd === "function" ? (props.pdfHeaderEnd as (tools:null)=>React.ReactNode)(null) : props.pdfHeaderEnd as React.ReactNode}<div data-testid="file-viewer" data-file={String(props.file || "")} data-filename={String(props.fileName || "")}/></div>;}}));
vi.mock('@/modules/knowledge/components/PdfTemporaryChat', () => ({default: (props:Record<string,unknown>) => {surfaces.chat=props;return null;}}));
vi.mock('@/runtime/mode', async()=>({...await vi.importActual<any>('@/runtime/mode'),isVocabularyEnabled:()=>surfaces.vocabularyEnabled}));
vi.mock('@/modules/vocabulary/DocumentVocabularyPanel',()=>({default:(props:Record<string,unknown>)=>{surfaces.vocabulary=props;return <div data-testid="document-vocabulary">Document vocabulary</div>;}}));
vi.mock('@/modules/vocabulary/AddVocabularyModal',()=>({default:(props:Record<string,unknown>)=>{surfaces.vocabularyModal=props;return null;}}));
const originalAdapter = axiosInstance.defaults.adapter;
afterEach(() => {surfaces.vocabularyEnabled=false;surfaces.viewerRenders=0;sessionStorage.clear();localStorage.removeItem('paper-reader:assistant-width');axiosInstance.defaults.adapter = originalAdapter;useDatasetPermissionStore.getState().clearDataset();vi.unstubAllGlobals();});
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

it('routes paper quick explanation to the assistant without a Local learning capability', async () => {
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'academic_papers'} as Dataset);
  axiosInstance.defaults.adapter = config => config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'paper.pdf'}})
    : new Promise(()=>{});
  const {container}=renderDocument();
  await waitFor(()=>expect(surfaces.viewer.fileName).toBe('paper.pdf'));
  const actions=surfaces.viewer.learningSelectionActions as Array<{key:string;disabled?:boolean}>;
  expect(actions.find(action=>action.key==='explanation')?.disabled).not.toBe(true);
  act(()=>{(surfaces.viewer.onLearningSelection as (key:string,selection:Record<string,unknown>)=>void)('explanation',{text:'attention',context:'Attention scores query-key pairs.',page:4,bbox:[10,20,30,40]});});
  await waitFor(()=>expect(surfaces.chat.selection).toMatchObject({text:'attention',context:'Attention scores query-key pairs.',page:4,bbox:[10,20,30,40],quickExplain:true,question:expect.any(String)}));
  expect(container.querySelector('.paper-chat-pane.is-collapsed')).toBeNull();
});

it('persists same-page reading progress without rerendering the PDF workspace',async()=>{
 useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'academic_papers'} as Dataset);
 axiosInstance.defaults.adapter=config=>config.url?.endsWith('/documents/paper')
  ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'paper.pdf'}})
  : new Promise(()=>{});
 renderDocument();
 await waitFor(()=>expect(surfaces.viewer.pdfViewPosition).toEqual({page:1,progress:0}));
 act(()=>{(surfaces.viewer.onPdfViewPositionChange as (position:{page:number;progress:number})=>void)({page:1,progress:.21});});
 await act(async()=>{await new Promise(resolve=>setTimeout(resolve,0));});
 const renders=surfaces.viewerRenders;
 act(()=>{(surfaces.viewer.onPdfViewPositionChange as (position:{page:number;progress:number})=>void)({page:1,progress:.42});});
 expect(surfaces.viewerRenders).toBe(renders);
 expect(JSON.parse(sessionStorage.getItem('lazymind:pdf-position:paper')||'null')).toEqual({page:1,progress:.42});
});

it('keeps an ordinary PDF in the document workspace with existing learning and chat actions',async()=>{
  surfaces.chat={};
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'general'} as Dataset);
  axiosInstance.defaults.adapter = config => config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'manual.pdf'}})
    : new Promise(()=>{});
  const {container}=renderDocument();
  await waitFor(()=>expect(container.querySelector('.paper-workspace-dock')).not.toBeNull());
  expect(container.querySelector('.is-paper-document')).toBeNull();
  expect(container.querySelector('.is-document-reader')).not.toBeNull();
  expect(container.querySelectorAll('.paper-workspace-dock button')).toHaveLength(3);
  expect(screen.queryByRole('button',{name:/Document details|文档详情/})).toBeNull();
  fireEvent.click(screen.getByRole('button',{name:/Document navigation|文档导航/}));
  expect(screen.getByRole('tab',{name:/References|参考文献/})).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button',{name:/Document navigation|文档导航/}));
  expect(surfaces.viewer.pdfIRTarget).toBeUndefined();
  expect(surfaces.viewer.onImportReferenceSelection).toBeTypeOf('function');
  expect(surfaces.viewer.onPdfTranslateSelection).toBeTypeOf('function');
  expect(surfaces.viewer.onLearningSelection).toBeTypeOf('function');
  expect(container.querySelector('.paper-chat-pane.is-collapsed')).not.toBeNull();
  expect(surfaces.chat.paperMode).toBeUndefined();
  fireEvent.click(screen.getByRole('button',{name:/^Document assistant$|^文档助手$/}));
  await waitFor(()=>expect(surfaces.chat.paperMode).toBe(false));
  act(()=>{(surfaces.viewer.onImportReferenceSelection as (selection:{text:string})=>void)({text:'Selected reference'});});
  expect(screen.getByRole('tab',{name:/References|参考文献/})).toBeVisible();
  expect(screen.getByRole('button',{name:/Document navigation|文档导航/})).toHaveAttribute('aria-pressed','true');
});

it('keeps non-PDF navigation available after the file type is known', async () => {
  axiosInstance.defaults.adapter = config => config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config, status: 200, statusText: 'OK', headers: {}, data: {display_name: 'notes.txt'}})
    : new Promise(() => {});
  const {container} = renderDocument();
  await waitFor(() => expect(container.querySelector('.paper-workspace-dock')).not.toBeNull());
  fireEvent.click(screen.getByRole('button',{name:/Document navigation|文档导航/}));
  expect(screen.getByRole('tab',{name:/References|参考文献/})).toBeVisible();
  expect(screen.queryByRole('status')).toBeNull();
});

it('does not show the previous document while the next document metadata loads', async () => {
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'general'} as Dataset);
  axiosInstance.defaults.adapter = config => config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'first.pdf'}})
    : new Promise(()=>{});
  render(<MemoryRouter initialEntries={['/doc/library/paper']}><Link to="/doc/library/next">Next document</Link><Routes><Route path="/doc/:knowledgeBaseId/:knowledgeId" element={<Detail/>}/></Routes></MemoryRouter>);
  await waitFor(()=>expect(surfaces.viewer.fileName).toBe('first.pdf'));
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


it('preserves an ordinary document translation draft when switching panels and reopening', async () => {
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'general'} as Dataset);
  axiosInstance.defaults.adapter = config => config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'manual.pdf'}})
    : new Promise(()=>{});
  renderDocument();
  fireEvent.click(await screen.findByRole('button',{name:/^Document assistant$|^文档助手$/}));
  fireEvent.click(screen.getByRole('button',{name:/^Translation$|^翻译$/}));
  const input=screen.getByRole('textbox',{name:'输入要翻译的文字'});
  fireEvent.change(input,{target:{value:'Keep this unfinished translation'}});
  fireEvent.click(screen.getByRole('button',{name:/^Document assistant$|^文档助手$/}));
  expect(input).not.toBeVisible();
  fireEvent.click(screen.getByRole('button',{name:/^Translation$|^翻译$/}));
  expect(input).toBeVisible();
  expect(input).toHaveValue('Keep this unfinished translation');
  fireEvent.click(screen.getByRole('button',{name:/Collapse document assistant|收起文档助手/}));
  expect(input).not.toBeVisible();
  fireEvent.click(screen.getByRole('button',{name:/^Translation$|^翻译$/}));
  expect(input).toBeVisible();
  expect(input).toHaveValue('Keep this unfinished translation');
});


it.each([false,true])('gates paper information vocabulary by the existing feature flag (%s)',async(enabled)=>{
  surfaces.vocabularyEnabled=enabled;
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'academic_papers'} as Dataset);
  axiosInstance.defaults.adapter = config => config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'paper.pdf'}})
    : new Promise(()=>{});
  const {container}=renderDocument();
  fireEvent.click(await screen.findByRole('button',{name:/Document details|文档详情/}));
  if(enabled){
    expect(await screen.findByTestId('document-vocabulary')).toBeInTheDocument();
    expect(surfaces.vocabulary).toEqual(expect.objectContaining({documentId:'paper',refreshToken:0}));
    act(()=>{(surfaces.vocabularyModal.onAdded as ()=>void)();});
    expect(surfaces.vocabulary).toEqual(expect.objectContaining({documentId:'paper',refreshToken:1}));
    expect(container.querySelector('.paper-workspace-dock button[aria-pressed="true"]')).toHaveAccessibleName(/Document details|文档详情/);
  }else expect(screen.queryByTestId('document-vocabulary')).not.toBeInTheDocument();
  expect(container.querySelectorAll('.paper-workspace-dock button')).toHaveLength(4);
});

it('opens vocabulary from the paper reader with the selected word and source evidence',async()=>{
  surfaces.vocabularyEnabled=true;
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'academic_papers'} as Dataset);
  axiosInstance.defaults.adapter=config=>config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'paper.pdf'}})
    : new Promise(()=>{});
  renderDocument();
  await screen.findByRole('button',{name:/Document details|文档详情/});
  const selection={text:'resilient',context:'A resilient approach improves accuracy.',contextTranslation:'一种有韧性的方法提高了准确性。',page:3};
  act(()=>{(surfaces.viewer.onAddVocabularySelection as (value:typeof selection)=>void)(selection);});
  expect(surfaces.vocabularyModal).toEqual(expect.objectContaining({selection,datasetId:'library',documentId:'paper',context:selection.context}));
  act(()=>{(surfaces.vocabularyModal.onClose as ()=>void)();});
  expect(surfaces.vocabularyModal.selection).toBeNull();
});

it('lets the paper reader choose a word from a phrase while retaining source context',async()=>{
  surfaces.vocabularyEnabled=true;
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'academic_papers'} as Dataset);
  axiosInstance.defaults.adapter=config=>config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'paper.pdf'}})
    : new Promise(()=>{});
  renderDocument();
  await screen.findByRole('button',{name:/Document details|文档详情/});
  const selection={text:'resilient approach',context:'A resilient approach improves accuracy.',page:3};
  act(()=>{(surfaces.viewer.onAddVocabularySelection as (value:typeof selection)=>void)(selection);});
  expect(surfaces.vocabularyModal.selection).toBeNull();
  fireEvent.click(await screen.findByRole('button',{name:'加入生词'}));
  await waitFor(()=>expect(surfaces.vocabularyModal.selection).toEqual({...selection,text:'resilient'}));
  expect(surfaces.vocabularyModal).toEqual(expect.objectContaining({datasetId:'library',documentId:'paper',context:selection.context}));
});

it('opens full-document translation from the sidebar and preserves the selection draft', async () => {
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'general'} as Dataset);
  axiosInstance.defaults.adapter=config=>config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'manual.pdf'}})
    : new Promise(()=>{});
  renderDocument();
  fireEvent.click(await screen.findByRole('button',{name:/^Translation$|^翻译$/}));
  fireEvent.change(screen.getByRole('textbox',{name:'输入要翻译的文字'}),{target:{value:'Keep my draft'}});
  fireEvent.click(screen.getByText('全文翻译'));
  expect(screen.queryByRole('textbox',{name:'输入要翻译的文字'})).toBeNull();
  fireEvent.click(screen.getByRole('button',{name:'开始全文翻译'}));
  expect(await screen.findByRole('dialog',{name:'翻译文档'})).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button',{name:'取 消'}));
  fireEvent.click(screen.getByText('划词翻译'));
  expect(screen.getByRole('textbox',{name:'输入要翻译的文字'})).toHaveValue('Keep my draft');
});

it('shows document translation progress only inside the full-translation view',async()=>{
 useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'general'} as Dataset);
 axiosInstance.defaults.adapter=config=>{
  const data=config.url?.endsWith('/documents/paper')?{display_name:'manual.pdf'}
   :config.url?.endsWith('/pdf-capabilities')?{data:{is_pdf:true,reader_ready:true,pdf_kind:'native_text',translations:[],jobs:[{id:'job',kind:'TRANSLATION_PDF',status:'RUNNING',stage:'TRANSLATING',progress:17}]}}
   :undefined;
  return data?Promise.resolve({config,status:200,statusText:'OK',headers:{},data}):new Promise(()=>{});
 };
 renderDocument();
 await screen.findByRole('button',{name:/^Translation$|^翻译$/});
 await waitFor(()=>expect(screen.queryByText('正在翻译文档')).not.toBeInTheDocument());
 fireEvent.click(screen.getByRole('button',{name:/^Translation$|^翻译$/}));
 expect(screen.queryByText('正在翻译文档')).not.toBeInTheDocument();
 fireEvent.click(screen.getByText('全文翻译'));
 expect(await screen.findByText('正在翻译文档')).toBeVisible();
 fireEvent.click(screen.getByText('划词翻译'));
 expect(screen.queryByText('正在翻译文档')).not.toBeInTheDocument();
});

it('resizes the sidebar to half the workspace and restores widths above 640px', async () => {
  localStorage.setItem('paper-reader:assistant-width','750');
  vi.stubGlobal('ResizeObserver',class { constructor(private callback:ResizeObserverCallback) {} observe(element:Element){this.callback([{contentRect:{width:1600,height:1000},target:element} as ResizeObserverEntry],this as unknown as ResizeObserver);} disconnect(){} unobserve(){} });
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'general'} as Dataset);
  axiosInstance.defaults.adapter=config=>config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'manual.pdf'}})
    : new Promise(()=>{});
  const {container}=renderDocument();
  fireEvent.click(await screen.findByRole('button',{name:/^Translation$|^翻译$/}));
  const divider=screen.getByRole('separator',{name:/调整助手宽度|Resize assistant/});
  expect(divider).toHaveAttribute('aria-valuenow','750');
  fireEvent.keyDown(divider,{key:'End'});
  expect(divider).toHaveAttribute('aria-valuenow','800');
  expect((container.querySelector('.knowledge-paper-workspace') as HTMLElement).style.getPropertyValue('--paper-assistant-width')).toBe('800px');
  localStorage.removeItem('paper-reader:assistant-width');
});


it('reads a cached full translation in the sidebar while retaining the original document', async () => {
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'general'} as Dataset);
  axiosInstance.defaults.adapter=config=> {
    const data=config.url?.endsWith('/documents/paper') ? {display_name:'original.pdf',file_url:'/original.pdf'}
      : config.url?.endsWith('/pdf-capabilities') ? {data:{is_pdf:true,reader_ready:true,pdf_kind:'native_text',jobs:[],translations:[{id:'translated',kind:'TRANSLATION_PDF',cache_key:'cache',filename:'translated.pdf',target_language:'zh',provider_type:'llm',created_at:'2026-11-09T00:00:00Z'}]}}
      : undefined;
    return data ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data}) : new Promise(()=>{});
  };
  const {container}=renderDocument();
  fireEvent.click(await screen.findByRole('button',{name:/^Translation$|^翻译$/}));
  fireEvent.click(screen.getByText('全文翻译'));
  await waitFor(()=>expect(container.querySelector('.knowledge-full-translation-viewer [data-filename="translated.pdf"]')).not.toBeNull());
  expect(container.querySelector('.paper-reader-pane [data-testid="file-viewer"]')?.getAttribute('data-file')).toContain('/original.pdf');
  expect(container.querySelector('.knowledge-full-translation-viewer [data-testid="file-viewer"]')?.getAttribute('data-file')).toContain('/pdf-artifacts/translated:content');
  fireEvent.click(screen.getByRole('button',{name:'半屏阅读'}));
  expect(screen.getByRole('separator')).toHaveAttribute('aria-valuenow','720');
  fireEvent.click(screen.getByText('划词翻译'));
  expect(container.querySelector('.knowledge-full-translation-viewer')).toBeNull();
  fireEvent.click(screen.getByText('全文翻译'));
  expect(container.querySelector('.knowledge-full-translation-viewer [data-filename="translated.pdf"]')).not.toBeNull();
});

it('shows a completed background translation in the sidebar without replacing the source', async () => {
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'general'} as Dataset);
  let capabilityReads=0;
  const artifact={id:'completed',kind:'TRANSLATION_PDF',cache_key:'cache',filename:'completed.pdf',target_language:'zh',provider_type:'llm',created_at:'2026-11-09T00:00:00Z'};
  axiosInstance.defaults.adapter=config=> {
    let data:unknown;
    if(config.url?.endsWith('/documents/paper')) data={display_name:'original.pdf',file_url:'/original.pdf'};
    if(config.url?.endsWith('/pdf-capabilities')) {
      const ready=++capabilityReads>1;
      data={data:{is_pdf:true,reader_ready:true,pdf_kind:'native_text',translations:ready?[artifact]:[],jobs:[{id:'job',kind:'TRANSLATION_PDF',status:ready?'READY':'RUNNING',stage:ready?'READY':'TRANSLATING',progress:ready?100:30,artifact_id:ready?'completed':undefined}]}};
    }
    return data ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data}) : new Promise(()=>{});
  };
  const {container}=renderDocument();
  await waitFor(()=>expect(container.querySelector('.knowledge-full-translation-viewer [data-filename="completed.pdf"]')).not.toBeNull(),{timeout:4000});
  expect(container.querySelector('.paper-reader-pane [data-testid="file-viewer"]')?.getAttribute('data-file')).toContain('/original.pdf');
  expect(screen.getByRole('region',{name:'全文翻译'})).toBeVisible();
});


it('shows only the current translation and keeps it when switching the main searchable PDF', async () => {
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'general'} as Dataset);
  const older={id:'older',kind:'TRANSLATION_PDF',cache_key:'older',filename:'older.pdf',target_language:'zh',provider_type:'llm',created_at:'2026-11-08T00:00:00Z'};
  const newer={...older,id:'newer',filename:'newer.pdf',target_language:'en'};
  axiosInstance.defaults.adapter=config=> {
    const data=config.url?.endsWith('/documents/paper') ? {display_name:'original.pdf',file_url:'/original.pdf'}
      : config.url?.endsWith('/pdf-capabilities') ? {data:{is_pdf:true,reader_ready:true,pdf_kind:'image_only',jobs:[],translations:[older,newer],searchable_artifact:{...older,id:'searchable',kind:'SEARCHABLE_PDF',filename:'searchable.pdf'}}}
      : undefined;
    return data ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data}) : new Promise(()=>{});
  };
  const {container}=renderDocument();
  fireEvent.click(await screen.findByRole('button',{name:/^Translation$|^翻译$/}));
  fireEvent.click(screen.getByText('全文翻译'));
  expect(screen.queryByRole('combobox',{name:'全文翻译版本'})).not.toBeInTheDocument();
  expect(screen.getByRole('button',{name:'下载译本'})).toBeVisible();
  expect(screen.getByRole('button',{name:'加入文献附件'})).toBeVisible();
  expect(container.querySelector('.knowledge-full-translation-viewer [data-filename="newer.pdf"]')).not.toBeNull();
  fireEvent.click(screen.getByRole('button',{name:/Document actions and translation|文档操作与翻译/}));
  fireEvent.click(await screen.findByRole('button',{name:'返回普通 PDF'}));
  expect(container.querySelector('.paper-reader-pane [data-testid="file-viewer"]')?.getAttribute('data-file')).toContain('/searchable:content');
  fireEvent.click(screen.getByText('翻译版'));
  expect(screen.queryByRole('combobox',{name:'选择缓存译本'})).not.toBeInTheDocument();
  expect(container.querySelector('.knowledge-full-translation-viewer [data-filename="newer.pdf"]')).not.toBeNull();
});

it('previews a sidebar drag without rerendering the PDF or persisting each pointer movement', async () => {
  useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'general'} as Dataset);
  axiosInstance.defaults.adapter=config=>config.url?.endsWith('/documents/paper')
    ? Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'manual.pdf'}})
    : new Promise(()=>{});
  const {container}=renderDocument();
  fireEvent.click(await screen.findByRole('button',{name:/^Translation$|^翻译$/}));
  await act(async()=>{});
  const before=surfaces.viewerRenders;
  const write=vi.spyOn(localStorage,'setItem');
  vi.stubGlobal('PointerEvent',MouseEvent);
  const frames=new Map<number,FrameRequestCallback>();let sequence=0;
  vi.stubGlobal('requestAnimationFrame',(callback:FrameRequestCallback)=>{frames.set(++sequence,callback);return sequence;});
  vi.stubGlobal('cancelAnimationFrame',(id:number)=>frames.delete(id));
  const divider=screen.getByRole('separator',{name:/调整助手宽度|Resize assistant/});
  divider.setPointerCapture=vi.fn();
  Object.defineProperty(container.querySelector('.paper-reader-pane'),'clientWidth',{value:900});
  Object.defineProperty(container.querySelector('.paper-chat-pane'),'clientWidth',{value:398});
  fireEvent.pointerDown(divider,{button:0,clientX:600});
  for(const x of [580,550,500])fireEvent.pointerMove(divider,{clientX:x});
  act(()=>{for(const callback of frames.values())callback(0);frames.clear();});
  const workspace=container.querySelector('.knowledge-paper-workspace') as HTMLElement;
  expect(workspace.style.getPropertyValue('--paper-assistant-preview-width')).toBe('500px');
  expect(workspace.style.getPropertyValue('--paper-reader-drag-content-width')).toBe('900px');
  expect(workspace.style.getPropertyValue('--paper-assistant-drag-content-width')).toBe('398px');
  expect(workspace).toHaveAttribute('data-pane-resizing','true');
  expect(surfaces.viewerRenders).toBe(before);
  expect(write).not.toHaveBeenCalled();
  fireEvent.pointerUp(divider,{clientX:480});
  expect(workspace.style.getPropertyValue('--paper-assistant-preview-width')).toBe('');
  expect(workspace).not.toHaveAttribute('data-pane-resizing');
  expect(workspace.style.getPropertyValue('--paper-assistant-width')).toBe('520px');
  expect(write).toHaveBeenCalledTimes(1);
  expect(write).toHaveBeenLastCalledWith('paper-reader:assistant-width','520');
  write.mockRestore();
});
