import {fireEvent,render,screen} from '@testing-library/react';
import {beforeAll,describe,expect,it,vi} from 'vitest';
import i18n from '@/i18n';
import type {PdfIRBlock} from '@/modules/knowledge/api/pdfIR';
import {PdfTableLayer,hasTableGeometry} from './PdfTableLayer';
const block:PdfIRBlock={id:'t',type:'table',page:0,bbox:[.1,.2,.8,.7],table:{rows:[],captions:[],footnotes:[],related_blocks:[]}};
beforeAll(async()=>{await i18n.changeLanguage('en-US');});
describe('passive table layer',()=>{
 it('keeps its marker and does not prevent native page selection',()=>{
  const select=vi.fn(),pageDown=vi.fn();render(<div onPointerDown={pageDown} data-testid="page"><PdfTableLayer blocks={[block]} onSelect={select}/></div>);
  const page=screen.getByTestId('page');fireEvent.pointerDown(page);fireEvent.pointerUp(page);
  expect(pageDown).toHaveBeenCalled();expect(select).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button',{name:/Explore table/}));expect(select).toHaveBeenCalledWith(block);
 });
 it('rejects missing, out-of-range, or reversed geometry',()=>{
  expect(hasTableGeometry(block)).toBe(true);
  expect(hasTableGeometry({...block,bbox:undefined})).toBe(false);
  expect(hasTableGeometry({...block,bbox:[.9,0,.1,.3]})).toBe(false);
  expect(hasTableGeometry({...block,bbox:[0,-1,1,1]})).toBe(false);
 });
});


describe('original table page clicks',()=>{
 const setup=()=>{
  const select=vi.fn();
  const view=render(<div data-testid="page"><span data-testid="text">Original table</span><a href="#link">Linked cell</a><PdfTableLayer blocks={[block]} onSelect={select}/></div>);
  const page=screen.getByTestId('page');
  vi.spyOn(page,'getBoundingClientRect').mockReturnValue({left:10,top:20,width:200,height:400} as DOMRect);
  const click=(target:Element,extra={})=>{fireEvent.pointerDown(target,{clientX:60,clientY:140,button:0,...extra});fireEvent.pointerUp(target,{clientX:60,clientY:140,button:0,...extra});fireEvent.click(target,{clientX:60,clientY:140,button:0,...extra});};
  return {select,view,page,click};
 };
 it('opens from the actual PDF text layer using current page geometry and callback',()=>{
  const {select,view,page,click}=setup();click(screen.getByTestId('text'));expect(select).toHaveBeenCalledWith(block);
  const current=vi.fn();view.rerender(<div data-testid="page"><span data-testid="text">Original table</span><PdfTableLayer blocks={[block]} onSelect={current}/></div>);
  click(screen.getByTestId('text'));expect(current).toHaveBeenCalledWith(block);expect(select).toHaveBeenCalledTimes(1);
  expect(page).toBeInTheDocument();
 });
 it('ignores links, modifiers, native selection, and drag gestures without preventing them',()=>{
  const {select,page,click}=setup();click(screen.getByRole('link'));click(page,{ctrlKey:true});click(page,{shiftKey:true});
  const selection=vi.spyOn(window,'getSelection').mockReturnValue({toString:()=> 'selected source text'} as Selection);click(page);selection.mockRestore();
  fireEvent.pointerDown(page,{clientX:60,clientY:140,button:0});fireEvent.pointerMove(page,{clientX:100,clientY:160,buttons:1});fireEvent.pointerUp(page,{clientX:60,clientY:140,button:0});fireEvent.click(page,{clientX:60,clientY:140,button:0});
  expect(select).not.toHaveBeenCalled();
 });
});
