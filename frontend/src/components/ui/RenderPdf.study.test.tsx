import {act,fireEvent,render as renderView,screen,waitFor} from '@testing-library/react';
import {beforeEach,describe,expect,it,vi} from 'vitest';
import {useEffect,type ReactNode} from 'react';
import type {PdfIRDocument} from '@/modules/knowledge/api/pdfIR';
const state=vi.hoisted(()=>({pageLoads:new Map<number,(width:number,height:number)=>void>(),document:undefined as unknown,status:'ready',failLoad:false,deferLoad:false,load:undefined as undefined|(()=>void),pdfFile:undefined as unknown,text:undefined as undefined|((page:number)=>Promise<{items:Array<{str:string}>}>)}));
vi.mock('react-i18next',async original=>({...await original<typeof import('react-i18next')>(),useTranslation:()=>({t:(key:string)=>key})}));
vi.mock('react-pdf',()=>({pdfjs:{GlobalWorkerOptions:{}},Document:({children,onLoadSuccess,onLoadError,file}:{file:unknown;children:ReactNode;onLoadSuccess:(value:unknown)=>void;onLoadError:(error:Error)=>void})=>{state.pdfFile=file;useEffect(()=>{if(state.failLoad){onLoadError(new Error("Invalid PDF"));return;}state.load=()=>onLoadSuccess({numPages:3,getPage:async(page:number)=>({view:[0,0,600,800],getTextContent:()=>state.text?state.text(page):Promise.resolve({items:[{str:page===3?'needle result':'ordinary text'}]})})});if(!state.deferLoad)state.load();},[file]);return <>{children}</>;},Page:({width,pageNumber,onLoadSuccess}:{width:number;pageNumber:number;onLoadSuccess:(page:unknown)=>void})=> {state.pageLoads.set(pageNumber,(w,h)=>onLoadSuccess({getViewport:()=>({width:w,height:h})}));return <div data-testid="rendered-pdf-page" data-width={width}/>;}}));
vi.mock('@/modules/knowledge/api/pdfIR',async original=>({...await original<typeof import('@/modules/knowledge/api/pdfIR')>(),usePdfIR:()=>({state,starting:false,start:vi.fn()})}));
vi.mock('./PdfFormulaStudy',()=>({PdfFormulaStudy:()=> <aside aria-label="formula-study"/>}));
import RenderPdf from './RenderPdf';
function render(ui:ReactNode){const view=renderView(ui);const tools=screen.queryByRole('button',{name:'阅读工具'});if(tools)fireEvent.click(tools);return view;}
const target={dataset:'d',document:'one'};
function document(hash='hash'):PdfIRDocument{return {version:1,projection_version:2,source_sha256:hash,parser:{name:'mineru',model:'vlm'},coordinate_space:'normalized_top_left',pages:[{index:0}],blocks:[{id:'formula',type:'formula',page:0,latex:'x',bbox:[.1,.1,.3,.2]},{id:'table',type:'table',page:0,bbox:[.1,.4,.8,.8],table:{rows:[{cells:[{text:'Value',row_span:1,col_span:1}]}],captions:[],footnotes:[],related_blocks:[]}}]};}
beforeEach(()=>{state.pageLoads.clear();state.failLoad=false;state.text=undefined;state.deferLoad=false;sessionStorage.clear();state.document=document();state.status='ready';});
describe('reader study integration',()=>{
 it('switches between formula and table studies instead of opening two panels',async()=>{
  render(<RenderPdf fileData="test.pdf" pdfIRTarget={target} pdfSourceHash="hash"/>);
  fireEvent.click(screen.getByRole('button',{name:'pdfFormula.openCatalog'}));
  expect(screen.getByRole('complementary',{name:'formula-study'})).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button',{name:'pdfTable.openCatalog'}));
  expect(screen.queryByRole('complementary',{name:'formula-study'})).not.toBeInTheDocument();
  expect(screen.getByRole('complementary',{name:'pdfTable.study'})).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button',{name:'pdfFormula.openCatalog'}));
  expect(screen.queryByRole('complementary',{name:'pdfTable.study'})).not.toBeInTheDocument();
  await act(async()=>{});
 });
 it('closes the table study when the source changes and suppresses mismatched structures',async()=>{
  const view=render(<RenderPdf fileData="test.pdf" pdfIRTarget={target} pdfSourceHash="hash"/>);
  fireEvent.click(screen.getByRole('button',{name:'pdfTable.openCatalog'}));
  state.document=document('next');
  view.rerender(<RenderPdf fileData="next.pdf" pdfIRTarget={target} pdfSourceHash="next"/>);
  expect(screen.queryByRole('complementary',{name:'pdfTable.study'})).not.toBeInTheDocument();
  view.rerender(<RenderPdf fileData="mismatch.pdf" pdfIRTarget={target} pdfSourceHash="different"/>);
  expect(screen.queryByRole('button',{name:'pdfTable.openCatalog'})).not.toBeInTheDocument();
  await act(async()=>{});
 });
});

it('publishes only source-verified IR and returns to original for an external block jump',async()=>{
 const notify=vi.fn();const props={fileData:'test.pdf',pdfIRTarget:target,pdfSourceHash:'hash',onPdfIRDocumentChange:notify};
 const view=render(<RenderPdf {...props}/>);
 expect(notify).toHaveBeenLastCalledWith(state.document);

 expect(screen.queryByRole('tablist')).toBeNull();
 view.rerender(<RenderPdf {...props} pdfNavigateTo={{id:'table',requestId:1}}/>);
 await waitFor(()=>expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toBeInTheDocument());
 expect(view.container.querySelector('.pdf-study-highlight')).toBeInTheDocument();
 view.rerender(<RenderPdf {...props} pdfSourceHash="unverified"/>);
 expect(notify).toHaveBeenLastCalledWith(undefined);
});

it('retains rendered pages and source while resizing, hiding and restoring the reader',async()=>{
 const callbacks:Array<()=>void>=[];
 vi.stubGlobal('ResizeObserver',class{constructor(callback:()=>void){callbacks.push(callback)}observe(){}disconnect(){}});
 const width=vi.spyOn(HTMLElement.prototype,'clientWidth','get').mockReturnValue(800);
 const height=vi.spyOn(HTMLElement.prototype,'clientHeight','get').mockReturnValue(600);
 const view=render(<RenderPdf fileData="test.pdf" pdfIRTarget={target} pdfSourceHash="hash"/>);
 await waitFor(()=>expect(screen.getAllByTestId('rendered-pdf-page')[0]).toHaveAttribute('data-width','1200'));
 const page=screen.getAllByTestId('rendered-pdf-page')[0];
 const source=state.pdfFile;
 expect(page.closest('[data-pdf-page-index]')).toHaveStyle({width:'752px'});
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.nextPage'}));

 width.mockReturnValue(0);height.mockReturnValue(0);act(()=>callbacks.forEach(callback=>callback()));
 expect(screen.getAllByTestId('rendered-pdf-page')[0]).toHaveAttribute('data-width','1200');
 width.mockReturnValue(600);height.mockReturnValue(500);act(()=>callbacks.forEach(callback=>callback()));
 await waitFor(()=>expect(screen.getAllByTestId('rendered-pdf-page')[0]).toHaveAttribute('data-width','1200'));
 expect(screen.getAllByTestId('rendered-pdf-page')[0]).toBe(page);
 expect(page.closest('[data-pdf-page-index]')).toHaveStyle({width:'564px'});
 expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toHaveValue(2);
 expect(state.pdfFile).toBe(source);
 width.mockReturnValue(390);act(()=>callbacks.forEach(callback=>callback()));
 expect(page.closest('[data-pdf-page-index]')).toHaveStyle({width:'358px'});
 width.mockReturnValue(1600);act(()=>callbacks.forEach(callback=>callback()));
 expect(page.closest('[data-pdf-page-index]')).toHaveStyle({width:'960px'});
 expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toHaveValue(2);
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomIn'}));
 expect(page.closest('[data-pdf-page-index]')).toHaveStyle({width:'1056px'});
 expect(screen.getAllByTestId('rendered-pdf-page')[0]).toBe(page);
 view.unmount();width.mockRestore();height.mockRestore();vi.unstubAllGlobals();
});


it('navigates pages, changes zoom and searches actual PDF page text',async()=>{
 const view=render(<RenderPdf fileData="test.pdf" defaultPageWidth={600}/>);
 await waitFor(()=>expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toHaveValue(1));
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.nextPage'}));
 expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toHaveValue(2);
 const pageInput=screen.getByRole('spinbutton',{name:'pdfReader.page'});
 fireEvent.change(pageInput,{target:{value:'99'}});
 fireEvent.keyDown(pageInput,{key:'Enter'});
 expect(pageInput).toHaveValue(3);
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.previousPage'}));
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomIn'}));
 await waitFor(()=>expect(screen.getAllByTestId('rendered-pdf-page')[0]).toHaveAttribute('data-width','1200'));
 expect(screen.getAllByTestId('rendered-pdf-page')[0].closest('[data-pdf-page-index]')).toHaveStyle({width:'660px'});
 expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toHaveValue(2);
 fireEvent.click(screen.getByLabelText('pdfReader.search',{selector:'summary'}));
 fireEvent.change(screen.getByRole('searchbox',{name:'pdfReader.search'}),{target:{value:'needle'}});
 fireEvent.submit(screen.getByRole('search'));
 await waitFor(()=>expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toHaveValue(3));
 view.unmount();
});

it('keeps zoom when verified parsing arrives and restores source-scoped reading preferences',async()=>{
 sessionStorage.clear();
 state.document=undefined;
 const props={fileData:"preferences.pdf",pdfIRTarget:target,pdfSourceHash:"preferences"};
 const view=render(<RenderPdf {...props}/>);
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomIn'}));

 state.document=document("preferences");
 view.rerender(<RenderPdf {...props}/>);
 expect(screen.queryByRole('tablist')).toBeNull();
 view.unmount();
 render(<RenderPdf {...props}/>);
 expect(screen.queryByRole('tablist')).toBeNull();

 expect(screen.getByRole('button',{name:'110%'})).toBeInTheDocument();
 await act(async()=>{});
 sessionStorage.clear();
});


it('downloads the original binary PDF and reports when a study occupies the side panel',async()=>{
 const create=vi.fn(()=> 'blob:original-pdf');const revoke=vi.fn();
 vi.stubGlobal('URL',Object.assign(URL,{createObjectURL:create,revokeObjectURL:revoke}));
 const click=vi.spyOn(HTMLAnchorElement.prototype,'click').mockImplementation(()=>{});
 const onStudyOpenChange=vi.fn();const file=new ArrayBuffer(12);
 render(<RenderPdf fileData={file} paperTitle="Real paper" pdfIRTarget={target} pdfSourceHash="hash" onStudyOpenChange={onStudyOpenChange}/>);
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.download'}));
 expect(create).toHaveBeenCalledWith(expect.any(Blob));
 expect(click).toHaveBeenCalledOnce();
 fireEvent.click(screen.getByRole('button',{name:'pdfFormula.openCatalog'}));
 expect(onStudyOpenChange).toHaveBeenLastCalledWith(true);
 click.mockRestore();vi.unstubAllGlobals();
 await act(async()=>{});
});

it('opens an external page request in the original PDF without requiring parsed IR',async()=>{
 state.document=undefined;
 const props={fileData:"page-request.pdf",pdfIRTarget:target};
 const view=render(<RenderPdf {...props}/>);
 await act(async()=>{});

 view.rerender(<RenderPdf {...props} pdfNavigateToPage={{page:2,requestId:1}}/>);
 expect(screen.queryByRole('tablist')).toBeNull();
 expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toHaveValue(2);
 await act(async()=>{});
});


it('keeps transferable PDF bytes for downloading and blocks navigation until the new source loads',async()=>{
 const file=new ArrayBuffer(16);
 const view=render(<RenderPdf fileData={file}/>);
 await act(async()=>{});
 expect(state.pdfFile).not.toBe(file);
 structuredClone(state.pdfFile,{transfer:[state.pdfFile as ArrayBuffer]});
 expect(file.byteLength).toBe(16);
 state.deferLoad=true;
 view.rerender(<RenderPdf fileData="pending.pdf" pdfNavigateToPage={{page:3,requestId:1}}/>);
 expect(screen.getByRole('button',{name:'pdfReader.nextPage'})).toBeDisabled();
 act(()=>state.load?.());
 expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toHaveValue(3);
 await act(async()=>{});
});

it('preserves the current page on zoom despite an unchanged controlled position',async()=>{
 const view=render(<RenderPdf fileData="controlled.pdf" defaultPageWidth={600} viewPosition={{page:1,progress:0}}/>);
 await act(async()=>{});
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.nextPage'}));
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomIn'}));
 expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toHaveValue(2);
 view.rerender(<RenderPdf fileData="controlled.pdf" defaultPageWidth={480} viewPosition={{page:1,progress:0}}/>);
 expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toHaveValue(2);
});

it('does not restore overview for a PDF without structured-reading support',async()=>{
 sessionStorage.setItem('pdf-reader:::plain',JSON.stringify({mode:'insight',zoom:1}));
 render(<RenderPdf fileData="plain.pdf" pdfSourceHash="plain"/>);
 expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toBeInTheDocument();
 await act(async()=>{});
});


it('keeps PDF reading visible when source hashing replaces the loaded buffer',async()=>{
 state.document=undefined;
 const view=render(<RenderPdf fileData={new ArrayBuffer(4)} pdfIRTarget={target}/>);

 view.rerender(<RenderPdf fileData={new ArrayBuffer(4)} pdfIRTarget={target} pdfSourceHash="newly-hashed"/>);
 expect(screen.queryByRole('tablist')).toBeNull();
 await act(async()=>{});
});

it('stops searching more PDF pages after unmount',async()=>{
 let resolve!:(value:{items:Array<{str:string}>})=>void;
 state.text=vi.fn(()=>new Promise<{items:Array<{str:string}>}>(done=>{resolve=done;}));
 const view=render(<RenderPdf fileData="cancel-search.pdf"/>);
 await act(async()=>{});
 fireEvent.click(screen.getByLabelText('pdfReader.search',{selector:'summary'}));
 fireEvent.change(screen.getByRole('searchbox',{name:'pdfReader.search'}),{target:{value:'needle'}});
 fireEvent.submit(screen.getByRole('search'));
 await act(async()=>{});
 view.unmount();
 await act(async()=>resolve({items:[{str:'needle'}]}));
 expect(state.text).toHaveBeenCalledTimes(1);
});

 it('retains header slots without overview or bilingual reflow',async()=>{
 sessionStorage.setItem('pdf-reader:d:one:hash',JSON.stringify({mode:'insight',zoom:1}));
 const view=render(<RenderPdf fileData="test.pdf" pdfIRTarget={target} pdfSourceHash="hash" pdfHeaderStart={<button>Library</button>} pdfHeaderEnd={<button>Workspace</button>}/>);
 expect(screen.queryAllByRole('tab')).toHaveLength(0);
 expect(screen.queryByRole('tablist')).toBeNull();
 expect(screen.queryByRole('button',{name:'pdfReader.bilingual'})).toBeNull();
 expect(view.container.querySelector('.pdf-bilingual-reader')).toBeNull();
 expect(view.container.querySelector('.pdf-reader-header-start')).toContainElement(screen.getByRole('button',{name:'Library'}));
 await act(async()=>{});
 });

it('keeps the library action available when PDF decoding fails',async()=>{
 state.failLoad=true;const back=vi.fn();
 render(<RenderPdf fileData="broken.pdf" pdfHeaderStart={<button onClick={back}>Library</button>} pdfHeaderEnd={<button>Assistant</button>}/>);
 expect(await screen.findByText(/Invalid PDF/)).toBeInTheDocument();
 expect(screen.getAllByRole('button',{name:'Library'})).toHaveLength(1);
 fireEvent.click(screen.getByRole('button',{name:'Library'}));expect(back).toHaveBeenCalledOnce();
 expect(screen.getByRole('button',{name:'Assistant'})).toBeInTheDocument();
});

it('keeps the captured quote while typing, never offers reference import, and uses white pages at zoom',async()=>{
 const ask=vi.fn(),importReference=vi.fn();
 const view=render(<RenderPdf fileData="selection.pdf" onAskSelection={ask} onImportReferenceSelection={importReference} gapBackground="#777" referenceActions={[{referenceId:'ref',rawText:'Source quote',kind:'download',page:1,bbox:[0,0,600,800]}]}/>);
 await act(async()=>{});
 const page=screen.getAllByTestId('rendered-pdf-page')[0];
 const text=window.document.createElement('span');text.textContent='Source quote';text.dataset.referenceId='ref';page.append(text);
 const range=window.document.createRange();range.selectNodeContents(text);
 Object.defineProperty(range,'getBoundingClientRect',{value:()=>({left:100,top:200,right:200,bottom:220,width:100,height:20})});
 Object.defineProperty(range,'getClientRects',{value:()=>[]});
 window.getSelection()?.removeAllRanges();window.getSelection()?.addRange(range);
 fireEvent.mouseUp(text);
 const input=screen.getByRole('textbox',{name:'向 AI 提问或记笔记'});
 input.focus();fireEvent.change(input,{target:{value:'Explain this result'}});
 window.getSelection()?.removeAllRanges();window.document.dispatchEvent(new Event('selectionchange'));
 fireEvent.mouseUp(input);
 expect(input).toHaveValue('Explain this result');
 expect(screen.queryByText('下载并加入知识库')).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'向 AI 提问'}));
 expect(ask).toHaveBeenCalledWith(expect.objectContaining({text:'Source quote',question:'Explain this result',page:1}));
 expect(importReference).not.toHaveBeenCalled();
 expect(screen.queryByRole('dialog',{name:'选中文本操作'})).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomOut'}));
 expect(page.closest('[data-pdf-page-index]')).toHaveStyle({background:'#ffffff',boxShadow:'none'});
 expect(view.container.querySelector('.pdf-reader-with-study')?.firstElementChild).toHaveStyle({backgroundColor:'#ffffff'});
});

it('keeps all PDF controls in a top-right popover without a second header row',async()=>{
 const view=renderView(<RenderPdf fileData="tools.pdf" pdfIRTarget={target} pdfSourceHash="hash" pdfHeaderStart={<button>Library</button>} pdfHeaderEnd={<button>Document actions</button>}/>);
 expect(screen.queryByRole('spinbutton',{name:'pdfReader.page'})).toBeNull();
 const tools=screen.getByRole('button',{name:'阅读工具'});
 expect(view.container.querySelector('.pdf-reader-header-end')).toContainElement(tools);
 fireEvent.click(tools);
 expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toBeInTheDocument();
 expect(view.container.querySelector('.pdf-reader-header-end')).toContainElement(screen.getByRole('button',{name:'pdfReader.download'}));
 expect(view.container.querySelector('.pdf-reader-workspace > .pdf-reader-toolbar')).toBeNull();
 fireEvent.keyDown(tools,{key:'Escape'});
 expect(screen.queryByRole('spinbutton',{name:'pdfReader.page'})).toBeNull();
 await act(async()=>{});
});
it('preserves the reading anchor when a newly mounted page reveals a different height',async()=>{
 const width=vi.spyOn(HTMLElement.prototype,'clientWidth','get').mockReturnValue(800);
 const height=vi.spyOn(HTMLElement.prototype,'clientHeight','get').mockReturnValue(600);
 const view=render(<RenderPdf fileData="mixed-pages.pdf" defaultPageWidth={600}/>);await act(async()=>{});
 fireEvent.change(screen.getByRole('spinbutton',{name:'pdfReader.page'}),{target:{value:'3'}});
 const viewport=view.container.querySelector('.pdf-reader-with-study')!.firstElementChild as HTMLElement;
 const scroll=vi.spyOn(viewport,'scrollTo');scroll.mockClear();
 act(()=>state.pageLoads.get(2)!(600,1000));
 expect(screen.getByRole('spinbutton',{name:'pdfReader.page'})).toHaveValue(3);
 expect(scroll).toHaveBeenLastCalledWith({top:1840,behavior:'auto'});
 view.unmount();width.mockRestore();height.mockRestore();
});
