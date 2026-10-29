import {describe,expect,it} from 'vitest';
import type {PdfIRBlock} from '@/modules/knowledge/api/pdfIR';
import {tableDataCandidates} from './pdfTableAssociations';
const table=(id:string,number:string,values:string[]):PdfIRBlock=>({id,type:'table',page:0,table:{number,rows:[{cells:values.map(text=>({text,row_span:1,col_span:1}))}],captions:[],footnotes:[],related_blocks:[]}});
const paragraph=(text:string,id='p'):PdfIRBlock=>({id,type:'text',text,page:6,bbox:[.1,.2,.7,.6]});
describe('conservative numeric table associations',()=>{
 it('requires three distinct exact decimals and keeps original source identity and geometry',()=>{
  const selected=table('t','5',['49.52','48.92','55.24']);const p=paragraph('Next, we compare 49.52, 48.92 and 55.24.');
  const result=tableDataCandidates(selected,[selected,p]);
  expect(result).toEqual([{block:p,matchedValues:['49.52','48.92','55.24']}]);expect(result[0].block).toBe(p);
  expect(tableDataCandidates(selected,[selected,paragraph('49.52 49.52 48.92')])).toEqual([]);
  expect(tableDataCandidates(selected,[selected,paragraph('149.52 48.920 55.240')])).toEqual([]);
 });
 it('rejects shared-number ties and a stronger competing table',()=>{
  const selected=table('t','5',['49.52','48.92','55.24']);const p=paragraph('49.52 48.92 55.24 84.85');
  expect(tableDataCandidates(selected,[selected,table('other','4',['49.52','48.92','55.24']),p])).toEqual([]);
  expect(tableDataCandidates(selected,[selected,table('other','4',['49.52','48.92','55.24','84.85']),p])).toEqual([]);
  expect(tableDataCandidates(selected,[selected,table('other','4',['55.24','84.85']),p])).toHaveLength(1);
 });
 it('does not override explicit references to another table, including lists',()=>{
  const selected=table('t','5',['49.52','48.92','55.24']);
  for(const prefix of ['Table 4','Tables 5 and 4','Tables 5, 4','表 4'])expect(tableDataCandidates(selected,[selected,paragraph(`${prefix}: 49.52 48.92 55.24`)])).toEqual([]);
 });
 it('does not add candidates when explicit mentions already exist, nor reuse caption text',()=>{
  const selected=table('t','5',['49.52','48.92','55.24']);const p=paragraph('49.52 48.92 55.24');
  selected.table!.related_blocks=[{block_id:'p',relation:'caption'}];expect(tableDataCandidates(selected,[selected,p])).toEqual([]);
  selected.table!.related_blocks=[{block_id:'elsewhere',relation:'mention'}];expect(tableDataCandidates(selected,[selected,p])).toEqual([]);
 });
 it('limits results to three strongest original paragraphs with stable tie order',()=>{
  const selected=table('t','5',['49.52','48.92','55.24','84.85']);
  const blocks=[selected,...['a','b','c','d'].map(id=>paragraph('49.52 48.92 55.24',id)),paragraph('49.52 48.92 55.24 84.85','best')];
  expect(tableDataCandidates(selected,blocks).map(result=>result.block.id)).toEqual(['best','a','b']);
 });
});
