import type {PdfIRBlock} from '@/modules/knowledge/api/pdfIR';
export interface TableDataCandidate {block:PdfIRBlock;matchedValues:string[]}
function decimalValues(text:string):Set<string> {
 // Keep spelling/signs exact; 48.920 is not silently equated with 48.92.
 return new Set(text.match(/(?<![\p{L}\p{N}_.,])[-+]?\d+\.\d+(?![\p{L}\p{N}_]|\.\d)/gu)||[]);
}
function explicitTableNumbers(text:string):string[] {
 const token='[A-Z]?\\d+(?:\\.\\d+)?[A-Z]?';
 const references=new RegExp(`(?:\\bTables?\\s+|表\\s*)(${token}(?:\\s*(?:,|，|、|and|&|to|和|及|与|至|[-–])\\s*(?:Tables?\\s+|表\\s*)?${token})*)(?![\\w]|\\.\\d)`,'gi');
 const numbers:string[]=[];
 for(const match of text.matchAll(references))for(const number of match[1].match(new RegExp(token,'gi'))||[])numbers.push(number.toLowerCase());
 return numbers;
}
/** Numeric coincidence is only a reading clue, never promoted to an IR relation. */
export function tableDataCandidates(selected:PdfIRBlock,allBlocks:PdfIRBlock[]):TableDataCandidate[] {
 if(!selected.table||selected.table.related_blocks.some(reference=>reference.relation==='mention'))return [];
 const tables=Array.from(new Map([...allBlocks.filter(block=>block.type==='table'&&block.table),selected].map(block=>[block.id,block])).values());
 const values=new Map(tables.map(table=>[table.id,decimalValues(table.table!.rows.flatMap(row=>row.cells.map(cell=>cell.text)).join(' '))]));
 const targetValues=values.get(selected.id)!;if(targetValues.size<3)return [];
 const captions=new Set(tables.flatMap(table=>table.table!.related_blocks.filter(reference=>reference.relation==='caption').map(reference=>reference.block_id)));
 const captionTexts=new Set(tables.flatMap(table=>table.table!.captions));
 const candidates:TableDataCandidate[]=[];
 for(const block of allBlocks){
  if(block.type!=='text'||!block.text||block.text.length>30000||block.page<0||captions.has(block.id)||captionTexts.has(block.text))continue;
  const references=explicitTableNumbers(block.text);
  if(references.some(number=>number!==selected.table!.number?.toLowerCase()))continue;
  const paragraphValues=decimalValues(block.text);
  const matchedValues=Array.from(paragraphValues).filter(value=>targetValues.has(value));
  if(matchedValues.length<3)continue;
  const ambiguous=tables.some(table=>table.id!==selected.id&&Array.from(paragraphValues).filter(value=>values.get(table.id)!.has(value)).length>=matchedValues.length);
  if(!ambiguous)candidates.push({block,matchedValues});
 }
 return candidates.sort((a,b)=>b.matchedValues.length-a.matchedValues.length).slice(0,3);
}
