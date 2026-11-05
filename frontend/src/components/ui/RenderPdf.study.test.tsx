import {act,fireEvent,render as renderView,screen,waitFor} from '@testing-library/react';
import {beforeEach,describe,expect,it,vi} from 'vitest';
import {useEffect,type ReactNode} from 'react';
import type {PdfIRDocument} from '@/modules/knowledge/api/pdfIR';
const state=vi.hoisted(()=>({nativeClick:undefined as undefined|((item:{pageNumber:number;dest?:unknown[]})=>void),pageLoads:new Map<number,(width:number,height:number)=>void>(),document:undefined as unknown,status:'ready',failLoad:false,deferLoad:false,load:undefined as undefined|(()=>void),pdfFile:undefined as unknown,text:undefined as undefined|((page:number)=>Promise<{items:Array<{str:string}>}>)}));
vi.mock('react-i18next',async original=>({...await original<typeof import('react-i18next')>(),useTranslation:()=>({t:(key:string)=>key})}));
vi.mock('react-pdf',()=>({pdfjs:{GlobalWorkerOptions:{}},Document:({children,onLoadSuccess,onLoadError,file,onItemClick}:{file:unknown;onItemClick?:typeof state.nativeClick;children:ReactNode;onLoadSuccess:(value:unknown)=>void;onLoadError:(error:Error)=>void})=>{state.pdfFile=file;state.nativeClick=onItemClick;useEffect(()=>{if(state.failLoad){onLoadError(new Error("Invalid PDF"));return;}state.load=()=>onLoadSuccess({numPages:3,getPage:async(page:number)=>({view:[0,0,600,800],getViewport:()=>({width:600,height:800,convertToViewportPoint:(x:number,y:number)=>[x,800-y]}),getTextContent:()=>state.text?state.text(page):Promise.resolve({items:[{str:page===3?'needle result':'ordinary text'}]})})});if(!state.deferLoad)state.load();},[file]);return <>{children}</>;},Page:({width,pageNumber,onLoadSuccess,devicePixelRatio}:{width:number;pageNumber:number;onLoadSuccess:(page:unknown)=>void;devicePixelRatio?:number})=> {state.pageLoads.set(pageNumber,(w,h)=>onLoadSuccess({getViewport:()=>({width:w,height:h})}));return <div data-testid="rendered-pdf-page" data-width={width} data-pixel-ratio={devicePixelRatio}/>;}}));
vi.mock('@/modules/knowledge/api/pdfIR',async original=>({...await original<typeof import('@/modules/knowledge/api/pdfIR')>(),usePdfIR:()=>({state,starting:false,start:vi.fn()})}));
vi.mock('./PdfVisualStudy',()=>({PdfVisualStudy:()=> <aside aria-label="visual-study"/>}));
vi.mock('./PdfFormulaStudy',()=>({PdfFormulaStudy:()=> <aside aria-label="formula-study"/>}));
import RenderPdf from './RenderPdf';
import {AUTH_USER_CHANGE_EVENT} from '@/components/auth';
function render(ui:ReactNode){const view=renderView(ui);const tools=screen.queryByRole('button',{name:'阅读工具'});if(tools)fireEvent.click(tools);return view;}
const target={dataset:'d',document:'one'};
function document(hash='hash'):PdfIRDocument{return {version:1,projection_version:2,source_sha256:hash,parser:{name:'mineru',model:'vlm'},coordinate_space:'normalized_top_left',pages:[{index:0}],blocks:[{id:'formula',type:'formula',page:0,latex:'x',bbox:[.1,.1,.3,.2]},{id:'table',type:'table',page:0,bbox:[.1,.4,.8,.8],table:{rows:[{cells:[{text:'Value',row_span:1,col_span:1}]}],captions:[],footnotes:[],related_blocks:[]}}]};}
function pageButton(page:number){return screen.getByRole('button',{name:`pdfReader.page ${page} / 3`});}
function expectPage(page:number){expect(pageButton(page)).toBeInTheDocument();}

beforeEach(()=>{state.pageLoads.clear();state.failLoad=false;state.text=undefined;state.deferLoad=false;sessionStorage.clear();state.document=document();state.status='ready';});

it('keeps overview selected when geometry changes after a consumed page navigation',async()=>{
 const props={fileData:'outline-resize.pdf',pdfIRTarget:target,pdfSourceHash:'hash',pdfNavigateToPage:{page:2,requestId:1}};
 const view=render(<RenderPdf {...props} defaultPageWidth={600}/>);
 await act(async()=>{});
 fireEvent.click(screen.getByRole('tab',{name:'pdfReader.insight'}));
 view.rerender(<RenderPdf {...props} defaultPageWidth={700}/>);
 expect(screen.getByRole('tab',{name:'pdfReader.insight'})).toHaveAttribute('aria-selected','true');
});

it('keeps overview selected when IR refreshes after a consumed block navigation',async()=>{
 const props={fileData:'outline-refresh.pdf',pdfIRTarget:target,pdfSourceHash:'hash',pdfNavigateTo:{id:'table',requestId:1}};
 const view=render(<RenderPdf {...props}/>);
 await act(async()=>{});
 fireEvent.click(screen.getByRole('tab',{name:'pdfReader.insight'}));
 state.document={...document()};
 view.rerender(<RenderPdf {...props}/>);
 expect(screen.getByRole('tab',{name:'pdfReader.insight'})).toHaveAttribute('aria-selected','true');
});

it('does not restore legacy highlights with an unknown account owner',async()=>{
 sessionStorage.setItem('pdf-note-highlights:d:one:hash',JSON.stringify([{id:'old',page:1,color:'blue',rects:[[.1,.2,.3,.23]]}]));
 const view=render(<RenderPdf fileData="legacy.pdf" pdfIRTarget={target} pdfSourceHash="hash"/>);
 await act(async()=>{});
 expect(view.container.querySelector('[data-pdf-note-highlight]')).toBeNull();
});

it('paints notes restored from the library without depending on browser session storage',async()=>{
 const view=render(<RenderPdf fileData="persisted-note.pdf" pdfIRTarget={target} pdfSourceHash="hash" savedNoteHighlights={[{id:'server-note',page:1,color:'blue',rects:[[.1,.2,.4,.24]]}]}/>);
 await act(async()=>{});
 expect(view.container.querySelector('[data-pdf-note-highlight="blue"]')).not.toBeNull();
});

it('opens a saved note at its anchored position and offers return to the reading position',async()=>{
 const props={fileData:'saved-location.pdf'};
 const view=render(<RenderPdf {...props}/>);
 await act(async()=>{});
 view.rerender(<RenderPdf {...props} pdfNavigateToPosition={{page:2,progress:.35,requestId:1}}/>);
 await waitFor(()=>expectPage(2));
 expect(screen.getByRole('button',{name:'返回阅读位置'})).toBeInTheDocument();
});

it('isolates saved and mounted note highlights when the account changes',async()=>{
 const previous=localStorage.getItem('lazymind:user');
 const signIn=(userId:string)=>{localStorage.setItem('lazymind:user',JSON.stringify({userId,token:`token-${userId}`,tenantId:'tenant'}));window.dispatchEvent(new Event(AUTH_USER_CHANGE_EVENT));};
 try {
  signIn('alice');
  const props={fileData:'shared.pdf',pdfIRTarget:target,pdfSourceHash:'hash',onNoteSelection:vi.fn()};
  const view=render(<RenderPdf {...props}/>);
  await act(async()=>{});
  const page=screen.getAllByTestId('rendered-pdf-page')[0];
  const text=window.document.createElement('span');text.textContent='A private annotation';page.append(text);
  const pageElement=page.closest('[data-pdf-page-index]')!;
  vi.spyOn(pageElement,'getBoundingClientRect').mockReturnValue({left:0,top:0,right:600,bottom:800,width:600,height:800,x:0,y:0,toJSON:()=>({})});
  const range=window.document.createRange();range.selectNodeContents(text);
  const rect={left:60,top:160,right:180,bottom:184,width:120,height:24};
  Object.defineProperty(range,'getBoundingClientRect',{value:()=>rect});
  Object.defineProperty(range,'getClientRects',{value:()=>[rect]});
  window.getSelection()?.removeAllRanges();window.getSelection()?.addRange(range);
  fireEvent.mouseUp(text);
  fireEvent.click(screen.getByRole('button',{name:'高亮并记笔记'}));
  expect(view.container.querySelector('[data-pdf-note-highlight]')).not.toBeNull();
  act(()=>signIn('bob'));
  expect(view.container.querySelector('[data-pdf-note-highlight]')).toBeNull();
  view.unmount();
  const reopened=render(<RenderPdf {...props}/>);
  await act(async()=>{});
  expect(reopened.container.querySelector('[data-pdf-note-highlight]')).toBeNull();
  act(()=>signIn('alice'));
  expect(reopened.container.querySelector('[data-pdf-note-highlight]')).not.toBeNull();
  reopened.unmount();
 } finally {
  if(previous===null)localStorage.removeItem('lazymind:user');else localStorage.setItem('lazymind:user',previous);
  window.dispatchEvent(new Event(AUTH_USER_CHANGE_EVENT));
  window.getSelection()?.removeAllRanges();
 }
});

it('keeps a paper overview with the source abstract and an unavailable quick-read state',async()=>{
 state.document={...document(),blocks:[
  {id:'title',type:'title',page:0,text:'A useful paper',bbox:[.1,.1,.9,.2]},
  {id:'abstract',type:'abstract',page:0,text:'Abstract: The method predicts a useful result.',bbox:[.1,.2,.9,.4]},
  ...document().blocks,
 ]};
 const view=render(<RenderPdf fileData="overview.pdf" pdfIRTarget={target} pdfSourceHash="hash" paperTitle="Fallback title"/>);
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.nextPage'}));
 fireEvent.click(screen.getByRole('tab',{name:'pdfReader.insight'}));
 expect(screen.getByRole('heading',{name:'A useful paper'})).toBeInTheDocument();
 expect(screen.getByText('The method predicts a useful result.')).toBeInTheDocument();
 expect(screen.getByText('论文速读暂时不可用')).toBeInTheDocument();
 expect(view.container.querySelector('.pdf-reader-with-study')).toHaveStyle({visibility:'hidden'});
 fireEvent.click(screen.getByRole('tab',{name:'pdfReader.reading'}));
 expectPage(2);
 expect(view.container.querySelector('.pdf-reader-with-study')).toHaveStyle({visibility:'visible'});
});

it('opens the overview before document parsing is ready',()=>{
 state.document=undefined;
 render(<RenderPdf fileData="unparsed.pdf" pdfIRTarget={target} paperTitle="Unparsed paper"/>);
 fireEvent.click(screen.getByRole('tab',{name:'pdfReader.insight'}));
 expect(screen.getByRole('heading',{name:'Unparsed paper'})).toBeInTheDocument();
 expect(screen.getByText('论文速读暂时不可用')).toBeInTheDocument();
});
describe('reader study integration',()=>{
 it('explains the parser requirement and links to its settings before full reading tools are available',async()=>{
  state.document=undefined;state.status='not_requested';
  render(<RenderPdf fileData="unparsed.pdf" pdfIRTarget={target} pdfSourceHash="hash"/>);
  expect(screen.getByText('pdfFormula.serviceRequired')).toBeInTheDocument();
  expect(screen.getByRole('link',{name:'pdfFormula.configureService'})).toHaveAttribute('href',expect.stringContaining('section=model_defaults&service=parsing'));
  await act(async()=>{});
 });
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

 expect(screen.getByRole('tablist')).toBeInTheDocument();
 view.rerender(<RenderPdf {...props} pdfNavigateTo={{id:'table',requestId:1}}/>);
 await waitFor(()=>expect(screen.getByRole('button',{name:/pdfReader.page/})).toBeInTheDocument());
 expect(view.container.querySelector('.pdf-study-highlight')).toBeInTheDocument();
 view.rerender(<RenderPdf {...props} pdfSourceHash="unverified"/>);
 expect(notify).toHaveBeenLastCalledWith(undefined);
});

it('retains rendered pages and source while resizing, hiding and restoring the reader',async()=>{
 const callbacks:Array<()=>void>=[];
 vi.stubGlobal('ResizeObserver',class{constructor(callback:()=>void){callbacks.push(callback)}observe(){}disconnect(){}});
 const width=vi.spyOn(HTMLElement.prototype,'clientWidth','get').mockReturnValue(800);
 const height=vi.spyOn(HTMLElement.prototype,'clientHeight','get').mockReturnValue(600);
 const view=render(<RenderPdf fileData="test.pdf" pdfIRTarget={target} pdfSourceHash="hash" paperMode/>);
 await waitFor(()=>expect(screen.getAllByTestId('rendered-pdf-page')[0]).toHaveAttribute('data-width','1200'));
 const page=screen.getAllByTestId('rendered-pdf-page')[0];
 const source=state.pdfFile;
 expect(page.closest('[data-pdf-page-index]')).toHaveStyle({width:'780px'});
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.nextPage'}));

 width.mockReturnValue(0);height.mockReturnValue(0);act(()=>callbacks.forEach(callback=>callback()));
 expect(screen.getAllByTestId('rendered-pdf-page')[0]).toHaveAttribute('data-width','1200');
 width.mockReturnValue(600);height.mockReturnValue(500);act(()=>callbacks.forEach(callback=>callback()));
 await waitFor(()=>expect(screen.getAllByTestId('rendered-pdf-page')[0]).toHaveAttribute('data-width','1200'));
 expect(screen.getAllByTestId('rendered-pdf-page')[0]).toBe(page);
 expect(page.closest('[data-pdf-page-index]')).toHaveStyle({width:'580px'});
 expectPage(2);
 expect(state.pdfFile).toBe(source);
 width.mockReturnValue(390);act(()=>callbacks.forEach(callback=>callback()));
 expect(page.closest('[data-pdf-page-index]')).toHaveStyle({width:'370px'});
 width.mockReturnValue(1600);act(()=>callbacks.forEach(callback=>callback()));
 expect(page.closest('[data-pdf-page-index]')).toHaveStyle({width:'1152px'});
 expectPage(2);
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomIn'}));
 expect(page.closest('[data-pdf-page-index]')).toHaveStyle({width:'1267px'});
 expect(screen.getAllByTestId('rendered-pdf-page')[0]).toBe(page);
 view.unmount();width.mockRestore();height.mockRestore();vi.unstubAllGlobals();
});


it('navigates pages and changes zoom without search controls',async()=>{
 const view=render(<RenderPdf fileData="test.pdf" defaultPageWidth={600} paperMode/>);
 await waitFor(()=>expectPage(1));
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.nextPage'}));
 expectPage(2);
 fireEvent.click(pageButton(2));
 const pageInput=screen.getByRole('spinbutton',{name:'pdfReader.page'});
 fireEvent.change(pageInput,{target:{value:'99'}});
 fireEvent.keyDown(pageInput,{key:'Enter'});
 expectPage(3);
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.previousPage'}));
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomIn'}));
 await waitFor(()=>expect(screen.getAllByTestId('rendered-pdf-page')[0]).toHaveAttribute('data-width','1200'));
 expect(screen.getAllByTestId('rendered-pdf-page')[0].closest('[data-pdf-page-index]')).toHaveStyle({width:'792px'});
 expectPage(2);
 expect(screen.queryByRole('searchbox')).toBeNull();
 view.unmount();
});

it('renders paper PDFs slightly larger than source size while the toolbar reads 100%',()=>{
 const view=render(<RenderPdf fileData="base-scale.pdf" defaultPageWidth={600} paperMode/>);
 const page=view.container.querySelector('[data-pdf-page-index]');
 expect(screen.getByRole('button',{name:'100%'})).toBeInTheDocument();
 expect(page).toHaveStyle({width:'720px'});
 expect(Number(screen.getAllByTestId('rendered-pdf-page')[0].dataset.pixelRatio)).toBeGreaterThanOrEqual(1.5);
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomIn'}));
 expect(screen.getByRole('button',{name:'110%'})).toBeInTheDocument();
 expect(page).toHaveStyle({width:'792px'});
 fireEvent.click(screen.getByRole('button',{name:'110%'}));
 const zoomInput=screen.getByRole('spinbutton',{name:'pdfReader.zoomPercent'});
 fireEvent.change(zoomInput,{target:{value:'100'}});
 fireEvent.keyDown(zoomInput,{key:'Enter'});
 expect(screen.getByRole('button',{name:'100%'})).toBeInTheDocument();
 expect(page).toHaveStyle({width:'720px'});
});

it('fits the paper page inside the reading pane at default zoom when an assistant is open',()=>{
 const callbacks:Array<()=>void>=[];
 vi.stubGlobal('ResizeObserver',class{constructor(callback:()=>void){callbacks.push(callback)}observe(){}disconnect(){}});
 const width=vi.spyOn(HTMLElement.prototype,'clientWidth','get').mockReturnValue(600);
 const height=vi.spyOn(HTMLElement.prototype,'clientHeight','get').mockReturnValue(500);
 const view=render(<RenderPdf fileData="sidebar.pdf" defaultPageWidth={600} paperMode/>);
 act(()=>callbacks.forEach(callback=>callback()));
 expect(view.container.querySelector('[data-pdf-page-index]')).toHaveStyle({width:'580px'});
 view.unmount();width.mockRestore();height.mockRestore();vi.unstubAllGlobals();
});

it('fits ordinary documents to their available width at the default zoom',()=>{
 const view=render(<RenderPdf fileData="ordinary.pdf" defaultPageWidth={600}/>);
 const page=view.container.querySelector('[data-pdf-page-index]');
 expect(screen.getByRole('button',{name:'100%'})).toBeInTheDocument();
 expect(page).toHaveStyle({width:'600px'});
 expect(screen.getByRole('navigation',{name:'paperShell.document'})).toBeInTheDocument();
});

it('keeps page navigation in the toolbar without covering each PDF page with a page badge',()=>{
 const view=render(<RenderPdf fileData="page-badge.pdf" defaultPageWidth={600}/>);
 expectPage(1);
 expect(view.container.querySelector('.pdf-reader-page-total')).toHaveTextContent('/ 3');
 expect(view.container.querySelector('[data-pdf-page-index="0"]')).not.toHaveTextContent('1/3');
});

it('pans the PDF viewport with the hand tool and leaves page controls available',async()=>{
 const view=render(<RenderPdf fileData="hand-pan.pdf" defaultPageWidth={600} paperMode/>);
 await waitFor(()=>expect(screen.getByRole('button',{name:'pdfReader.panPaper'})).toBeEnabled());
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.panPaper'}));
 expect(screen.getByRole('button',{name:'pdfReader.panPaper'})).toHaveAttribute('aria-pressed','true');
 const pane=view.container.querySelector<HTMLElement>('.pdf-reader-with-study > div')!;
 pane.scrollLeft=240;
 const before=pane.scrollLeft;
 fireEvent.pointerDown(pane,{button:0,pointerId:1,clientX:150,clientY:150});
 fireEvent.pointerMove(pane,{pointerId:1,clientX:105,clientY:150});
 expect(pane.scrollLeft).toBe(before+45);
 fireEvent.pointerUp(pane,{pointerId:1});
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.panPaper'}));
 expect(screen.getByRole('button',{name:'pdfReader.panPaper'})).toHaveAttribute('aria-pressed','false');
 expectPage(1);
});

it('minimizes a pinned formula and restores it from the six-slot dock',async()=>{
 localStorage.setItem('lazymind:user',JSON.stringify({userId:'pin-test',username:'pin-test',token:'pin-test-token',tenantId:'tenant'}));
 render(<RenderPdf fileData="pinned-formula.pdf" pdfIRTarget={target} pdfSourceHash="hash"/>);
 await waitFor(()=>expect(screen.getByRole('button',{name:'pdfReader.captureRegion'})).toBeEnabled());
 fireEvent.click(window.document.querySelector('.pdf-formula-trigger')!);
 fireEvent.click(screen.getByRole('button',{name:'钉住研读窗口'}));
 expect(screen.getByRole('dialog',{name:'原文解读'}).parentElement).toHaveClass('is-pinned');
 fireEvent.click(screen.getByRole('button',{name:'最小化研读窗口'}));
 expect(screen.queryByRole('dialog',{name:'原文解读'})).not.toBeInTheDocument();
 fireEvent.click(screen.getByRole('button',{name:/展开已收起内容/}));
 fireEvent.click(screen.getByRole('button',{name:/恢复公式/}));
 expect(screen.getByRole('dialog',{name:'原文解读'}).parentElement).toHaveClass('is-pinned');
 localStorage.removeItem('lazymind:user');
});

it('opens formula and table reading in a floating source dialog instead of a reader sidebar',()=>{
 const view=render(<RenderPdf fileData="source-popup.pdf" pdfIRTarget={target} pdfSourceHash="hash"/>);
 fireEvent.click(view.container.querySelector('.pdf-formula-trigger')!);
 expect(window.document.querySelector('[role="dialog"][aria-label="原文解读"]')).toBeInTheDocument();
 expect(view.container.querySelector('.pdf-reader-with-study > .pdf-formula-study')).toBeNull();
 fireEvent.click(window.document.querySelector('[aria-label="关闭原文解读"]')!);
 fireEvent.click(view.container.querySelector('.pdf-table-marker')!);
 expect(window.document.querySelector('[role="dialog"][aria-label="原文解读"]')).toBeInTheDocument();
 expect(view.container.querySelector('.pdf-reader-with-study > .pdf-table-study')).toBeNull();
});

it('keeps zoom when verified parsing arrives and restores source-scoped reading preferences',async()=>{
 sessionStorage.clear();
 state.document=undefined;
 const props={fileData:"preferences.pdf",pdfIRTarget:target,pdfSourceHash:"preferences"};
 const view=render(<RenderPdf {...props}/>);
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomIn'}));

 state.document=document("preferences");
 view.rerender(<RenderPdf {...props}/>);
 expect(screen.getByRole('tablist')).toBeInTheDocument();
 view.unmount();
 render(<RenderPdf {...props}/>);
 expect(screen.getByRole('tablist')).toBeInTheDocument();

 expect(screen.getByRole('button',{name:'110%'})).toBeInTheDocument();
 await act(async()=>{});
 sessionStorage.clear();
});


it('omits the PDF download action and still opens the study panel',async()=>{
 const onStudyOpenChange=vi.fn();
 render(<RenderPdf fileData={new ArrayBuffer(12)} pdfIRTarget={target} pdfSourceHash="hash" onStudyOpenChange={onStudyOpenChange}/>);
 expect(screen.queryByRole('button',{name:'pdfReader.download'})).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'pdfFormula.openCatalog'}));
 expect(onStudyOpenChange).toHaveBeenLastCalledWith(true);
 await act(async()=>{});
});

it('opens an external page request in the original PDF without requiring parsed IR',async()=>{
 state.document=undefined;
 const props={fileData:"page-request.pdf",pdfIRTarget:target};
 const view=render(<RenderPdf {...props}/>);
 await act(async()=>{});

 view.rerender(<RenderPdf {...props} pdfNavigateToPage={{page:2,requestId:1}}/>);
 expect(screen.getByRole('tablist')).toBeInTheDocument();
 expectPage(2);
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
 expectPage(3);
 await act(async()=>{});
});

it('preserves the current page on zoom despite an unchanged controlled position',async()=>{
 const view=render(<RenderPdf fileData="controlled.pdf" defaultPageWidth={600} viewPosition={{page:1,progress:0}}/>);
 await act(async()=>{});
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.nextPage'}));
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomIn'}));
 expectPage(2);
 view.rerender(<RenderPdf fileData="controlled.pdf" defaultPageWidth={480} viewPosition={{page:1,progress:0}}/>);
 expectPage(2);
});

it('does not restore overview for a PDF without structured-reading support',async()=>{
 sessionStorage.setItem('pdf-reader:::plain',JSON.stringify({mode:'insight',zoom:1}));
 render(<RenderPdf fileData="plain.pdf" pdfSourceHash="plain"/>);
 expect(screen.getByRole('button',{name:/pdfReader.page/})).toBeInTheDocument();
 await act(async()=>{});
});


it('keeps PDF reading visible when source hashing replaces the loaded buffer',async()=>{
 state.document=undefined;
 const view=render(<RenderPdf fileData={new ArrayBuffer(4)} pdfIRTarget={target}/>);

 view.rerender(<RenderPdf fileData={new ArrayBuffer(4)} pdfIRTarget={target} pdfSourceHash="newly-hashed"/>);
 expect(screen.getByRole('tablist')).toBeInTheDocument();
 await act(async()=>{});
});

 it('retains header slots and overview without bilingual reflow',async()=>{
 sessionStorage.setItem('pdf-reader:d:one:hash',JSON.stringify({mode:'insight',zoom:1}));
 const view=render(<RenderPdf fileData="test.pdf" pdfIRTarget={target} pdfSourceHash="hash" pdfHeaderStart={<button>Library</button>} pdfHeaderEnd={<button>Workspace</button>}/>);
 expect(screen.getAllByRole('tab')).toHaveLength(2);
 expect(screen.getByRole('tablist')).toBeInTheDocument();
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

it('keeps page and zoom controls in the header without search or download',async()=>{
 const view=renderView(<RenderPdf fileData="tools.pdf" pdfIRTarget={target} pdfSourceHash="hash" pdfHeaderStart={<button>Library</button>} pdfHeaderEnd={<button>Document actions</button>}/>);
 const header=view.container.querySelector('.pdf-reader-header');
 expect(header).toContainElement(pageButton(1));
 expect(header).toContainElement(screen.getByRole('button',{name:'pdfReader.zoomIn'}));
 expect(screen.queryByRole('searchbox')).toBeNull();
 expect(screen.queryByRole('button',{name:'pdfReader.download'})).toBeNull();
 expect(view.container.querySelector('.pdf-reader-workspace > .pdf-reader-toolbar')).toBeNull();
 await act(async()=>{});
});
it('sends reading progress to the outer page after scrolling settles',async()=>{
 const onViewPositionChange=vi.fn();
 const view=render(<RenderPdf fileData="progress.pdf" onViewPositionChange={onViewPositionChange}/>);
 const viewport=view.container.querySelector('.pdf-reader-with-study')!.firstElementChild as HTMLElement;
 await act(async()=>{});
 viewport.scrollTop=80;fireEvent.scroll(viewport);
 viewport.scrollTop=120;fireEvent.scroll(viewport);
 expect(onViewPositionChange).not.toHaveBeenCalled();
 await waitFor(()=>expect(onViewPositionChange).toHaveBeenCalledTimes(1));
 expect(onViewPositionChange).toHaveBeenCalledWith(expect.objectContaining({page:1,progress:expect.any(Number)}));
 view.unmount();
});
it('preserves the reading anchor when a newly mounted page reveals a different height',async()=>{
 const width=vi.spyOn(HTMLElement.prototype,'clientWidth','get').mockReturnValue(800);
 const height=vi.spyOn(HTMLElement.prototype,'clientHeight','get').mockReturnValue(600);
 const view=render(<RenderPdf fileData="mixed-pages.pdf" defaultPageWidth={600} paperMode/>);await act(async()=>{});
 fireEvent.click(pageButton(1));
 fireEvent.change(screen.getByRole('spinbutton',{name:'pdfReader.page'}),{target:{value:'3'}});
 fireEvent.keyDown(screen.getByRole('spinbutton',{name:'pdfReader.page'}),{key:'Enter'});
 const viewport=view.container.querySelector('.pdf-reader-with-study')!.firstElementChild as HTMLElement;
 const scroll=vi.spyOn(viewport,'scrollTo');scroll.mockClear();
 act(()=>state.pageLoads.get(2)!(600,1000));
 expectPage(3);
 expect(scroll).toHaveBeenLastCalledWith({top:2200,behavior:'auto'});
 view.unmount();width.mockRestore();height.mockRestore();
});


it('closes a table study without navigating away from the reading position',async()=>{
 const view=render(<RenderPdf fileData="return.pdf" pdfIRTarget={target} pdfSourceHash="hash"/>);
 fireEvent.click(screen.getByRole('button',{name:'pdfTable.openCatalog'}));
 fireEvent.change(screen.getByRole('combobox',{name:'pdfTable.catalog'}),{target:{value:'table'}});
 expect(screen.getByRole('dialog')).toBeInTheDocument();
 expect(screen.queryByRole('button',{name:'pdfFormula.returnReading'})).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'关闭原文解读'}));
 expect(screen.queryByRole('dialog')).toBeNull();
 view.unmount();
 await act(async()=>{});
});

it('scales zoom monotonically around the fitted reading baseline',async()=>{
 const width=vi.spyOn(HTMLElement.prototype,'clientWidth','get').mockReturnValue(800);
 const height=vi.spyOn(HTMLElement.prototype,'clientHeight','get').mockReturnValue(600);
 const view=render(<RenderPdf fileData="zoom-fit.pdf" paperMode/>);
 const page=view.container.querySelector('[data-pdf-page-index]');
 expect(page).toHaveStyle({width:'780px'});
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomOut'}));
 expect(page).toHaveStyle({width:'702px'});
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomIn'}));
 expect(page).toHaveStyle({width:'780px'});
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomIn'}));
 expect(page).toHaveStyle({width:'858px'});
 view.unmount();width.mockRestore();height.mockRestore();
 await act(async()=>{});
});

it('follows native PDF destinations without requiring a rendered target or parsed document',async()=>{
 state.document=undefined;
 const position=vi.fn();
 render(<RenderPdf fileData="native-links.pdf" onViewPositionChange={position}/>);
 await act(async()=>{});
 expect(state.nativeClick).toBeTypeOf('function');
 await act(async()=>{state.nativeClick?.({pageNumber:3,dest:[2,{name:'XYZ'},0,400,null]});});
 await waitFor(()=>expectPage(3));
 expect(position).toHaveBeenLastCalledWith({page:3,progress:.5});
});

it('keeps native link navigation working through the callback retained by react-pdf after a source change',async()=>{
 const position=vi.fn();
 const view=render(<RenderPdf fileData="first.pdf" onViewPositionChange={position}/>);
 const retainedClick=state.nativeClick;
 view.rerender(<RenderPdf fileData="second.pdf" onViewPositionChange={position}/>);
 await act(async()=>{retainedClick?.({pageNumber:2,dest:[1,{name:'Fit'}]});});
 await waitFor(()=>expectPage(2));
 expect(position).toHaveBeenLastCalledWith({page:2,progress:0});
});

it('opens a figure from the catalog without moving the PDF viewport',async()=>{
 state.document={...document(),blocks:[...document().blocks,{id:'figure',type:'image',page:2,bbox:[.1,.1,.8,.8],image:{number:'1',captions:['Figure 1'],footnotes:[],related_blocks:[]}}]};
 const view=render(<RenderPdf fileData="still.pdf" pdfIRTarget={target} pdfSourceHash="hash"/>);
 await act(async()=>{});
 const viewport=view.container.querySelector('.pdf-reader-with-study > div') as HTMLElement;
 const scroll=vi.spyOn(viewport,'scrollTo');scroll.mockClear();
 fireEvent.change(screen.getByRole('combobox',{name:'pdfReader.visuals'}),{target:{value:'figure'}});
 await act(async()=>{await new Promise(resolve=>setTimeout(resolve,150));});
 expect(screen.getByRole('complementary',{name:'visual-study'})).toBeInTheDocument();
 expect(scroll).not.toHaveBeenCalled();
 expectPage(1);
});

it('collects vocabulary from the original PDF selection with its page and sentence',async()=>{
 const add=vi.fn();
 render(<RenderPdf fileData="vocabulary.pdf" onAddVocabularySelection={add}/>);
 await act(async()=>{});
 const page=screen.getAllByTestId('rendered-pdf-page')[0];
 const text=window.document.createElement('span');text.textContent='A resilient approach improves accuracy.';page.append(text);
 Object.defineProperty(page.closest('[data-pdf-page-index]'),'innerText',{value:text.textContent});
 const range=window.document.createRange();range.setStart(text.firstChild!,2);range.setEnd(text.firstChild!,11);
 Object.defineProperty(range,'getBoundingClientRect',{value:()=>({left:100,top:200,right:200,bottom:220,width:100,height:20})});
 Object.defineProperty(range,'getClientRects',{value:()=>[]});
 window.getSelection()?.removeAllRanges();window.getSelection()?.addRange(range);
 fireEvent.mouseUp(text);
 fireEvent.click(screen.getByRole('button',{name:'加入生词'}));
 expect(add).toHaveBeenCalledWith(expect.objectContaining({text:'resilient',context:'A resilient approach improves accuracy.',page:1}));
 expect(screen.queryByRole('dialog',{name:'选中文本操作'})).toBeNull();
 window.getSelection()?.removeAllRanges();
});

it('connects vocabulary collection in the mounted overview to the paper reader action',async()=>{
 state.document={...document(),blocks:[{id:'abstract',type:'abstract',page:0,text:'Abstract: A resilient approach improves accuracy.'}]};
 const add=vi.fn();
 render(<RenderPdf fileData="overview-vocabulary.pdf" pdfIRTarget={target} pdfSourceHash="hash" onAddVocabularySelection={add}/>);
 await act(async()=>{});
 fireEvent.click(screen.getByRole('tab',{name:'pdfReader.insight'}));
 const text=screen.getByText('A resilient approach improves accuracy.');
 const range=window.document.createRange();range.selectNodeContents(text);
 window.getSelection()?.removeAllRanges();window.getSelection()?.addRange(range);
 fireEvent.mouseUp(text);
 fireEvent.click(screen.getByRole('button',{name:'加入生词'}));
 expect(add).toHaveBeenCalledWith({text:'A resilient approach improves accuracy.',context:'A resilient approach improves accuracy.',page:1});
 window.getSelection()?.removeAllRanges();
});
