import {fireEvent,render,screen,waitFor} from '@testing-library/react';
import {beforeAll,describe,expect,it,vi} from 'vitest';
import i18n from '@/i18n';
import type {PdfIRDocument} from '@/modules/knowledge/api/pdfIR';
import {PaperOutlineRail} from './PaperOutlineRail';
const document = {version:1,source_sha256:'hash',coordinate_space:'normalized_top_left',parser:{name:'mineru',model:'vlm'},pages:[],blocks:[
 {id:'intro',type:'heading',text:'1 Introduction',page:0},
 {id:'model',type:'heading',text:'2 Model',page:3},
 {id:'detail',type:'heading',text:'2.1 Architecture',page:4},
 {id:'body',type:'text',text:'A body paragraph.',page:4},
]} as PdfIRDocument;
beforeAll(async()=>{await i18n.changeLanguage('en-US');});
describe('PaperOutlineRail',()=>{
 it('opens on hover and navigates to real source block IDs',()=>{
  const navigate=vi.fn();render(<PaperOutlineRail document={document} currentPage={1} onNavigate={navigate}/>);
  expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
  fireEvent.mouseEnter(screen.getByRole('button',{name:'Outline'}));
  expect(screen.queryByText('A body paragraph.')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button',{name:/2.1 Architecture/}));
  expect(navigate).toHaveBeenCalledWith('detail');
  expect(screen.getByRole('navigation')).toBeVisible();
  fireEvent.click(screen.getByRole('button',{name:'1 Introduction'}));
  expect(navigate).toHaveBeenLastCalledWith('intro');
  expect(screen.getByRole('navigation')).toBeVisible();
 });
 it('tracks one-based PDF page changes against zero-based heading pages',()=>{
  const view=render(<PaperOutlineRail document={document} currentPage={4} onNavigate={vi.fn()}/>);
  fireEvent.click(screen.getByRole('button',{name:'Outline'}));
  expect(screen.getByRole('button',{name:/2 Model/})).toHaveAttribute('aria-current','location');
  view.rerender(<PaperOutlineRail document={document} currentPage={5} onNavigate={vi.fn()}/>);
  expect(screen.getByRole('button',{name:/2.1 Architecture/})).toHaveAttribute('aria-current','location');
  expect(screen.getByRole('button',{name:/2 Model/})).not.toHaveAttribute('aria-current');
 });
 it('opens on keyboard focus, moves focus with arrows and returns focus on Escape',async()=>{
  render(<PaperOutlineRail document={document} currentPage={4} onNavigate={vi.fn()}/>);
  const trigger=screen.getByRole('button',{name:'Outline'});
  fireEvent.focus(trigger);
  fireEvent.keyDown(trigger,{key:'ArrowDown'});
  await waitFor(()=>expect(screen.getByRole('button',{name:/2 Model/})).toHaveFocus());
  fireEvent.keyDown(screen.getByRole('button',{name:/2 Model/}),{key:'ArrowDown'});
  await waitFor(()=>expect(screen.getByRole('button',{name:/2.1 Architecture/})).toHaveFocus());
  fireEvent.keyDown(screen.getByRole('button',{name:/2.1 Architecture/}),{key:'Escape'});
  expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
 });
 it('supports touch-style click and dismisses on outside pointer interaction',()=>{
  render(<PaperOutlineRail document={document} currentPage={1} onNavigate={vi.fn()}/>);
  fireEvent.click(screen.getByRole('button',{name:'Outline'}));
  expect(screen.getByRole('navigation')).toBeVisible();
  fireEvent.pointerDown(window.document.body);
  expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
 });
 it('uses stable source order for headings on the same page and sorts earlier pages first',()=>{
  const source={...document,blocks:[
   {id:'late',type:'heading',text:'3 Results',page:8},
   {id:'first',type:'heading',text:'2 Method',page:3},
   {id:'second',type:'heading',text:'2.1 Detail',page:3},
  ]};
  render(<PaperOutlineRail document={source} currentPage={4} onNavigate={vi.fn()}/>);
  fireEvent.click(screen.getByRole('button',{name:'Outline'}));
  const chapters=screen.getAllByRole('listitem');
  expect(chapters.map(item=>item.textContent)).toEqual(['2 Method','2.1 Detail','3 Results']);
  expect(screen.getByRole('button',{name:/2.1 Detail/})).toHaveAttribute('aria-current','location');
 });
 it('scrolls the active chapter into view when opening and when the page changes',()=>{
  const original=HTMLElement.prototype.scrollIntoView;
  const scroll=vi.fn();
  HTMLElement.prototype.scrollIntoView=scroll;
  try {
   const view=render(<PaperOutlineRail document={document} currentPage={4} onNavigate={vi.fn()}/>);
   expect(scroll).not.toHaveBeenCalled();
   fireEvent.click(screen.getByRole('button',{name:'Outline'}));
   expect(scroll).toHaveBeenLastCalledWith({block:'nearest',inline:'nearest'});
   expect(scroll.mock.instances[scroll.mock.instances.length-1]).toBe(screen.getByRole('button',{name:/2 Model/}));
   view.rerender(<PaperOutlineRail document={document} currentPage={5} onNavigate={vi.fn()}/>);
   expect(scroll.mock.instances[scroll.mock.instances.length-1]).toBe(screen.getByRole('button',{name:/2.1 Architecture/}));
  } finally {
   HTMLElement.prototype.scrollIntoView=original;
  }
 });
 it('does not open when disabled',()=>{
  render(<PaperOutlineRail document={document} currentPage={1} onNavigate={vi.fn()} disabled/>);
  const trigger=screen.getByRole('button',{name:'Outline'});
  fireEvent.mouseEnter(trigger);
  fireEvent.click(trigger);
  expect(trigger).toBeDisabled();
  expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
 });
 it('shows an honest empty state instead of invented chapters',()=>{
  render(<PaperOutlineRail currentPage={1} onNavigate={vi.fn()}/>);
  fireEvent.click(screen.getByRole('button',{name:'Outline'}));
  expect(screen.getByText('This PDF has no available outline.')).toBeVisible();
  expect(screen.queryByRole('list')).not.toBeInTheDocument();
 });
});
it('keeps the clicked chapter active when several headings share its page',()=>{
 const source={...document,blocks:[{id:'first',type:'heading',text:'2 Method',page:3},{id:'second',type:'heading',text:'2.1 Detail',page:3}]};
 render(<PaperOutlineRail document={source} currentPage={4} onNavigate={vi.fn()}/>);
 fireEvent.click(screen.getByRole('button',{name:'Outline'}));
 fireEvent.click(screen.getByRole('button',{name:'2 Method'}));
 expect(screen.getByRole('button',{name:'2 Method'})).toHaveAttribute('aria-current','location');
 expect(screen.getByRole('button',{name:'2.1 Detail'})).not.toHaveAttribute('aria-current');
});
it('uses the same heading depth for ticks and expanded chapter indentation',()=>{
 const {container}=render(<PaperOutlineRail document={document} currentPage={1} onNavigate={vi.fn()}/>);
 const ticks=container.querySelectorAll('.paper-outline-rail__ticks > span');
 expect(Array.from(ticks).map(tick=>tick.getAttribute('data-depth'))).toEqual(['0','0','1']);
 fireEvent.click(screen.getByRole('button',{name:'Outline'}));
 expect(screen.getByRole('button',{name:'2.1 Architecture'})).toHaveStyle({paddingInlineStart:'26px'});
});
