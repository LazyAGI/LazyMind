import type {PdfIRTable, PdfIRTableCell} from '@/modules/knowledge/api/pdfIR';
export interface PlacedTableCell {key:string;row:number;column:number;rowSpan:number;colSpan:number;cell:PdfIRTableCell}
const span=(value:number)=>Number.isSafeInteger(value)&&value>0&&value<=1000?value:1;
/** Resolve original logical positions, including slots occupied by earlier row spans. */
export function layoutTable(table:PdfIRTable):PlacedTableCell[][] {
 const occupied=new Map<number,Set<number>>();
 return table.rows.map((row,rowIndex)=>{
  let column=0;
  return row.cells.map((cell,index)=>{
   const colSpan=span(cell.col_span),rowSpan=Math.min(span(cell.row_span),table.rows.length-rowIndex);
   while(Array.from({length:colSpan},(_,offset)=>column+offset).some(c=>occupied.get(rowIndex)?.has(c)))column++;
   const placed={key:`${rowIndex}:${index}`,row:rowIndex,column,rowSpan,colSpan,cell};
   for(let r=rowIndex;r<rowIndex+rowSpan;r++){
    const columns=occupied.get(r)||new Set<number>();
    for(let c=column;c<column+colSpan;c++)columns.add(c);
    occupied.set(r,columns);
   }
   column+=colSpan;return placed;
  });
 });
}
/** Only explicit header cells qualify. Data in a first row/column is never promoted. */
export function tableCellHeaders(rows:PlacedTableCell[][],selected:PlacedTableCell):{rows:string[];columns:string[]} {
 let topHeaders=0;
 while(topHeaders<rows.length&&rows[topHeaders].length>0&&rows[topHeaders].every(c=>c.cell.header))topHeaders++;
 const all=rows.flat(),columns:string[]=[],rowHeaders:string[]=[];
 for(const candidate of all){
  if(candidate.key===selected.key||!candidate.cell.header||!candidate.cell.text.trim())continue;
  if(candidate.row<topHeaders&&candidate.row+candidate.rowSpan<=selected.row&&candidate.column<selected.column+selected.colSpan&&candidate.column+candidate.colSpan>selected.column)columns.push(candidate.cell.text);
  if(candidate.row>=topHeaders&&candidate.row<=selected.row&&candidate.row+candidate.rowSpan>selected.row&&candidate.column+candidate.colSpan<=selected.column){
   // Row headers must form the leading header run; an interior <th> is ambiguous.
   const leading=all.filter(c=>c.row<=selected.row&&c.row+c.rowSpan>selected.row&&c.column<candidate.column);
   if(leading.every(c=>c.cell.header))rowHeaders.push(candidate.cell.text);
  }
 }
 return {rows:Array.from(new Set(rowHeaders)),columns:Array.from(new Set(columns))};
}
