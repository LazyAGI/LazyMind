import {act,fireEvent,render,screen,within} from '@testing-library/react';
import {afterEach,beforeAll,describe,expect,it,vi} from 'vitest';
import i18n from '@/i18n';
import type {PdfIRBlock} from '@/modules/knowledge/api/pdfIR';
import {PdfTableStudy} from './PdfTableStudy';
import {layoutTable,tableCellHeaders} from './pdfTableGeometry';
const block:PdfIRBlock={id:'table',type:'table',page:1,bbox:[.1,.2,.8,.7],table:{number:'2',captions:['Table 2. Results'],footnotes:['Measured in seconds.'],related_blocks:[{block_id:'mention',relation:'mention'},{block_id:'missing',relation:'caption'}],rows:[{cells:[{text:'Method',row_span:2,col_span:1,header:true},{text:'Results',row_span:1,col_span:2,header:true}]},{cells:[{text:'Fast',row_span:1,col_span:1,header:true},{text:'Slow',row_span:1,col_span:1,header:true}]},{cells:[{text:'Baseline',row_span:1,col_span:1,header:true},{text:'7',row_span:1,col_span:1},{text:'<img src=x onerror=alert(1)>',row_span:1,col_span:1}]}]}};
const mention:PdfIRBlock={id:'mention',type:'text',page:3,text:'Table 2 reports the measured results.',bbox:[.1,.1,.8,.2]};
const props={sourceKey:'source',blocks:[block],allBlocks:[block,mention],selected:block,onSelect:vi.fn(),onNavigate:vi.fn(),onReturn:vi.fn(),canReturn:true,onClose:vi.fn()};
beforeAll(async()=>{await i18n.changeLanguage('en-US');});
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks();});
describe('safe PDF table reading',()=>{
 it('places merged cells and derives only explicit header associations',()=>{
  const rows=layoutTable(block.table!);
  expect(rows[1][0].column).toBe(1);
  expect(tableCellHeaders(rows,rows[2][1])).toEqual({rows:['Baseline'],columns:['Results','Fast']});
  const plain=layoutTable({...block.table!,rows:[{cells:[{text:'Guess?',row_span:1,col_span:1},{text:'4',row_span:1,col_span:1}]}]});
  expect(tableCellHeaders(plain,plain[0][1])).toEqual({rows:[],columns:[]});
 });
 it('renders native spans, keeps HTML as text, and shows selected cell value and headers',()=>{
  const view=render(<PdfTableStudy {...props}/>);
  expect(screen.getByRole('columnheader',{name:'Method'})).toHaveAttribute('rowspan','2');
  expect(screen.getByRole('columnheader',{name:'Results'})).toHaveAttribute('colspan','2');
  expect(view.container.querySelector('img')).toBeNull();
  fireEvent.click(screen.getByRole('button',{name:'7'}));
  const detail=screen.getByRole('region',{name:'Selected cell'});
  expect(detail).toHaveTextContent('7');expect(detail).toHaveTextContent('Baseline');expect(detail).toHaveTextContent('Results');expect(detail).toHaveTextContent('Fast');
  expect(screen.getByRole('button',{name:'7'})).toHaveAttribute('aria-pressed','true');
 });
 it('navigates only to authoritative mention blocks and keeps unlocated captions honest',()=>{
  const navigate=vi.fn();render(<PdfTableStudy {...props} onNavigate={navigate}/>);
  expect(screen.getByText('Table 2. Results')).toBeInTheDocument();
  expect(screen.getByText(mention.text!)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button',{name:'Source · p. 4'}));
  expect(navigate).toHaveBeenCalledWith({id:mention.id,text:mention.text,page:3,bbox:mention.bbox});
  expect(screen.queryByText('missing')).toBeNull();
 });
 it('supports arrow-key cell navigation and resets selection on source change',()=>{
  const view=render(<PdfTableStudy {...props}/>);
  const value=screen.getByRole('button',{name:'7'});fireEvent.click(value);
  fireEvent.keyDown(value,{key:'ArrowRight'});
  expect(screen.getByRole('button',{name:'<img src=x onerror=alert(1)>'})).toHaveFocus();
  view.rerender(<PdfTableStudy {...props} sourceKey="new-source"/>);
  expect(screen.queryByRole('region',{name:'Selected cell'})).toBeNull();
 });
 it('does not treat an ordinary row as headers or select a cell during text selection',()=>{
  const selection=vi.spyOn(window,'getSelection').mockReturnValue({toString:()=> 'selected source text'} as Selection);
  render(<PdfTableStudy {...props}/>);fireEvent.click(screen.getByRole('button',{name:'7'}));
  expect(screen.queryByRole('region',{name:'Selected cell'})).toBeNull();selection.mockRestore();
 });
 it('shows only position and value when the source has no verified headers',()=>{
  const plain={...block,table:{...block.table!,rows:[{cells:[{text:'Value',row_span:1,col_span:1}]}]}};
  render(<PdfTableStudy {...props} blocks={[plain]} selected={plain}/>);
  fireEvent.click(screen.getByRole('button',{name:'Value'}));
  const detail=screen.getByRole('region',{name:'Selected cell'});
  expect(detail).toHaveTextContent('Row 1 · column 1');expect(detail).not.toHaveTextContent('headers');expect(detail).not.toHaveTextContent('Not identified');
 });
 it('does not invent table numbers for unnamed tables',()=>{
  const unnumbered={...block,table:{...block.table!,number:undefined}};
  render(<PdfTableStudy {...props} blocks={[unnumbered]} selected={unnumbered}/>);
  expect(within(screen.getByRole('combobox')).getByRole('option',{name:/Unnumbered table/})).toBeInTheDocument();
 });
});


describe('table context clues and reading position',()=>{
 it('separates unverified numeric clues and jumps to untouched original source geometry',()=>{
  const numeric={...block,table:{...block.table!,related_blocks:[],rows:[{cells:['49.52','48.92','55.24'].map(text=>({text,row_span:1,col_span:1}))}]}};
  const context={...mention,text:'Next, we analyze 49.52, 48.92 and 55.24.'};const navigate=vi.fn();
  render(<PdfTableStudy {...props} blocks={[numeric]} allBlocks={[numeric,context]} selected={numeric} onNavigate={navigate}/>);
  expect(screen.getByText('Data matches · association unverified')).toBeInTheDocument();expect(screen.getByText('Unverified association')).toBeInTheDocument();
  expect(screen.getByText(context.text)).toBeInTheDocument();fireEvent.click(screen.getByRole('button',{name:'Source · p. 4'}));
  expect(navigate).toHaveBeenCalledWith({id:context.id,text:context.text,page:context.page,bbox:context.bbox});
 });
 it('reveals selected table content below the sticky header on source/table change, not on cell selection',()=>{
  vi.useFakeTimers();const view=render(<PdfTableStudy {...props}/>);
  const panel=screen.getByRole('complementary'),section=panel.querySelector('.pdf-study-body > section')!,header=panel.querySelector('header')!;
  const scroll=vi.fn();Object.defineProperty(panel,'scrollTo',{configurable:true,value:scroll});
  vi.spyOn(panel,'getBoundingClientRect').mockReturnValue({top:0} as DOMRect);vi.spyOn(section,'getBoundingClientRect').mockReturnValue({top:150} as DOMRect);vi.spyOn(header,'getBoundingClientRect').mockReturnValue({height:60} as DOMRect);
  act(()=>vi.advanceTimersByTime(30));expect(scroll).toHaveBeenLastCalledWith({top:90,behavior:'auto'});
  fireEvent.click(screen.getByRole('button',{name:'7'}));act(()=>vi.advanceTimersByTime(30));expect(scroll).toHaveBeenCalledTimes(1);
  view.rerender(<PdfTableStudy {...props} sourceKey="another-source"/>);act(()=>vi.advanceTimersByTime(30));expect(scroll).toHaveBeenCalledTimes(2);expect(screen.queryByRole('region',{name:'Selected cell'})).toBeNull();
  view.rerender(<PdfTableStudy {...props} sourceKey="another-source" selected={{...block,id:'another-table'}}/>);act(()=>vi.advanceTimersByTime(30));expect(scroll).toHaveBeenCalledTimes(3);
 });
});
