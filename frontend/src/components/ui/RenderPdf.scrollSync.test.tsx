import {act,fireEvent,render,screen} from '@testing-library/react';
import {afterEach,beforeEach,expect,it,vi} from 'vitest';
import {useEffect,type ReactNode} from 'react';
import RenderPdf from './RenderPdf';
import {createPdfScrollSync} from './pdfScrollSync';

const pageLoads=vi.hoisted(()=>new Map<number,(width:number,height:number)=>void>());
vi.mock('react-i18next',async original=>({
  ...await original<typeof import('react-i18next')>(),
  useTranslation:()=>({t:(key:string)=>key}),
}));
vi.mock('react-pdf',()=>({
  pdfjs:{GlobalWorkerOptions:{}},
  Document:({children,file,onLoadSuccess}:{children:ReactNode;file:unknown;onLoadSuccess:(document:unknown)=>void})=>{
    useEffect(()=>{
      onLoadSuccess({numPages:3,getPage:async()=>({
        view:[0,0,600,800],
        getViewport:()=>({width:600,height:800,convertToViewportPoint:(x:number,y:number)=>[x,800-y]}),
      })});
    },[file]);
    return <div data-testid="pdf-document">{children}</div>;
  },
  Page:({pageNumber,onLoadSuccess}:{pageNumber:number;onLoadSuccess:(page:unknown)=>void})=>{
    pageLoads.set(pageNumber,(width,height)=>onLoadSuccess({
      getViewport:()=>({width,height,convertToViewportPoint:(x:number,y:number)=>[x,height-y]}),
    }));
    return null;
  },
}));
vi.mock('@/modules/knowledge/api/pdfIR',async original=>({
  ...await original<typeof import('@/modules/knowledge/api/pdfIR')>(),
  usePdfIR:()=>({state:{status:'not_requested'},starting:false}),
}));
vi.mock('./pdfPinStore',async original=>({
  ...await original<typeof import('./pdfPinStore')>(),
  readPdfPinBoard:vi.fn().mockResolvedValue([]),
  writePdfPinBoard:vi.fn().mockResolvedValue(true),
}));

beforeEach(()=>{
  pageLoads.clear();sessionStorage.clear();
  vi.spyOn(HTMLElement.prototype,'clientWidth','get').mockReturnValue(800);
  vi.spyOn(HTMLElement.prototype,'clientHeight','get').mockReturnValue(600);
  // Model the browser offset update. Tests dispatch its asynchronous scroll
  // event separately, after the layout effect has finished acknowledging it.
  vi.spyOn(HTMLElement.prototype,'scrollTo').mockImplementation(function(this:HTMLElement,options?:ScrollToOptions|number,y?:number){
    if(typeof options==='number'){this.scrollLeft=options;this.scrollTop=y??0;}
    else {if(options?.top!==undefined)this.scrollTop=options.top;if(options?.left!==undefined)this.scrollLeft=options.left;}
  });
});
afterEach(()=>{vi.restoreAllMocks();vi.unstubAllGlobals();});

it.each(['page measurement','zoom'])('keeps the original stable after translation %s and resumes user scroll sync',async trigger=>{
  const sync=createPdfScrollSync();
  const source=document.createElement('div');
  Object.defineProperty(source,'scrollHeight',{value:2440});
  sync.source.connect(source);
  const view=render(<RenderPdf fileData="translation-sync.pdf" defaultPageWidth={600} pdfToolbarMode="navigation" pdfScrollSync={sync.translation}/>);
  await act(async()=>{});
  const viewport=view.container.querySelector('.pdf-reader-with-study')!.firstElementChild as HTMLElement;
  const stage=screen.getByTestId('pdf-document').firstElementChild as HTMLElement;
  Object.defineProperty(viewport,'scrollHeight',{get:()=>parseFloat(stage.style.height)});
  source.scrollTop=1000;
  act(()=>sync.setEnabled(true));
  fireEvent.scroll(viewport);
  expect(viewport.scrollTop).toBe(1000);

  if(trigger==='page measurement')await act(async()=>pageLoads.get(2)!(600,1600));
  else fireEvent.click(screen.getByRole('button',{name:'pdfReader.zoomIn'}));
  expect(viewport.scrollTop).toBe(trigger==='page measurement'?1180:1098);
  fireEvent.scroll(viewport);
  expect(source.scrollTop).toBe(1000);

  viewport.scrollTop+=100;
  fireEvent.scroll(viewport);
  expect(source.scrollTop).toBeCloseTo(trigger==='page measurement'?892.121212:1059.769231);
  expect(source.scrollTop).not.toBe(1000);
});

it.each(['before resize','after resize'])('does not sync a taller translation viewport clamp when its scroll event arrives %s',async timing=>{
  let translationHeight=600;
  const frames:FrameRequestCallback[]=[];
  vi.stubGlobal('requestAnimationFrame',(callback:FrameRequestCallback)=>{frames.push(callback);return frames.length;});
  vi.stubGlobal('cancelAnimationFrame',vi.fn());
  vi.spyOn(HTMLElement.prototype,'clientHeight','get').mockImplementation(function(this:HTMLElement){
    return this.classList.contains('translation-sync-viewport')?translationHeight:600;
  });
  vi.spyOn(HTMLElement.prototype,'scrollHeight','get').mockImplementation(function(this:HTMLElement){
    const stage=this.querySelector('[data-testid="pdf-document"]')?.firstElementChild as HTMLElement|null;
    return Number.parseFloat(stage?.style.height||'0');
  });
  const sync=createPdfScrollSync();
  const source=document.createElement('div');
  Object.defineProperty(source,'scrollHeight',{value:2440});
  sync.source.connect(source);
  const view=render(<RenderPdf className="translation-sync-viewport" fileData="translation-resize.pdf" defaultPageWidth={600} pdfToolbarMode="navigation" pdfScrollSync={sync.translation}/>);
  await act(async()=>{});
  const viewport=view.container.querySelector('.translation-sync-viewport') as HTMLElement;
  let offset=0;
  Object.defineProperty(viewport,'scrollTop',{
    get:()=>{offset=Math.min(offset,Math.max(0,viewport.scrollHeight-translationHeight));return offset;},
    set:(top:number)=>{offset=Math.max(0,Math.min(top,viewport.scrollHeight-translationHeight));},
  });
  source.scrollTop=1000;
  act(()=>sync.setEnabled(true));
  fireEvent.scroll(viewport);
  expect(viewport.scrollTop).toBe(1000);

  translationHeight=1600;
  expect(viewport.scrollTop).toBe(840); // Browser clamps before ResizeObserver/resize handling.
  if(timing==='before resize'){
    fireEvent.scroll(viewport);
    expect(source.scrollTop).toBe(1000);
  }
  fireEvent(window,new Event('resize'));
  act(()=>{for(const frame of frames.splice(0))frame(100);});
  if(timing==='after resize')fireEvent.scroll(viewport);
  expect(source.scrollTop).toBe(1000);

  viewport.scrollTop=700;
  fireEvent.scroll(viewport);
  expect(source.scrollTop).toBeCloseTo(1533.333333);
});
