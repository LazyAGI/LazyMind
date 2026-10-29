import {act,fireEvent,render,screen,waitFor} from '@testing-library/react';
import {beforeEach,describe,expect,it,vi} from 'vitest';
import {useEffect,type ReactNode} from 'react';
import type {PdfIRDocument} from '@/modules/knowledge/api/pdfIR';
const state=vi.hoisted(()=>({document:undefined as unknown,status:'ready'}));
vi.mock('react-i18next',async original=>({...await original<typeof import('react-i18next')>(),useTranslation:()=>({t:(key:string)=>key})}));
vi.mock('react-pdf',()=>({pdfjs:{GlobalWorkerOptions:{}},Document:({children,onLoadSuccess}:{children:ReactNode;onLoadSuccess:(value:unknown)=>void})=>{useEffect(()=>{onLoadSuccess({numPages:1,getPage:async()=>({view:[0,0,600,800]})});},[]);return <>{children}</>;},Page:({width}:{width:number})=> <div data-testid="rendered-pdf-page" data-width={width}/>}));
vi.mock('@/modules/knowledge/api/pdfIR',async original=>({...await original<typeof import('@/modules/knowledge/api/pdfIR')>(),usePdfIR:()=>({state,starting:false,start:vi.fn()})}));
vi.mock('./PdfFormulaStudy',()=>({PdfFormulaStudy:()=> <aside aria-label="formula-study"/>}));
import RenderPdf from './RenderPdf';
const target={dataset:'d',document:'one'};
function document(hash='hash'):PdfIRDocument{return {version:1,projection_version:2,source_sha256:hash,parser:{name:'mineru',model:'vlm'},coordinate_space:'normalized_top_left',pages:[{index:0}],blocks:[{id:'formula',type:'formula',page:0,latex:'x',bbox:[.1,.1,.3,.2]},{id:'table',type:'table',page:0,bbox:[.1,.4,.8,.8],table:{rows:[{cells:[{text:'Value',row_span:1,col_span:1}]}],captions:[],footnotes:[],related_blocks:[]}}]};}
beforeEach(()=>{state.document=document();state.status='ready';});
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
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.bilingual'}));
 expect(screen.getByRole('button',{name:'pdfReader.bilingual'})).toHaveAttribute('aria-pressed','true');
 view.rerender(<RenderPdf {...props} pdfNavigateTo={{id:'table',requestId:1}}/>);
 await waitFor(()=>expect(screen.getByRole('button',{name:'pdfReader.original'})).toHaveAttribute('aria-pressed','true'));
 expect(view.container.querySelector('.pdf-study-highlight')).toBeInTheDocument();
 view.rerender(<RenderPdf {...props} pdfSourceHash="unverified"/>);
 expect(notify).toHaveBeenLastCalledWith(undefined);
});

it('preserves PDF rendering dimensions while hidden and resumes positive resize updates',async()=>{
 const callbacks:Array<()=>void>=[];
 vi.stubGlobal('ResizeObserver',class{constructor(callback:()=>void){callbacks.push(callback)}observe(){}disconnect(){}});
 const width=vi.spyOn(HTMLElement.prototype,'clientWidth','get').mockReturnValue(800);
 const height=vi.spyOn(HTMLElement.prototype,'clientHeight','get').mockReturnValue(600);
 const view=render(<RenderPdf fileData="test.pdf" pdfIRTarget={target} pdfSourceHash="hash"/>);
 await waitFor(()=>expect(screen.getByTestId('rendered-pdf-page')).toHaveAttribute('data-width','752'));
 fireEvent.click(screen.getByRole('button',{name:'pdfReader.bilingual'}));
 width.mockReturnValue(0);height.mockReturnValue(0);act(()=>callbacks.forEach(callback=>callback()));
 expect(screen.getByTestId('rendered-pdf-page')).toHaveAttribute('data-width','752');
 width.mockReturnValue(600);height.mockReturnValue(500);fireEvent.click(screen.getByRole('button',{name:'pdfReader.original'}));act(()=>callbacks.forEach(callback=>callback()));
 await waitFor(()=>expect(screen.getByTestId('rendered-pdf-page')).toHaveAttribute('data-width','564'));
 view.unmount();width.mockRestore();height.mockRestore();vi.unstubAllGlobals();
});
