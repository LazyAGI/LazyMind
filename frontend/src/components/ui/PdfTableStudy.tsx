import {useEffect,useMemo,useRef,useState} from 'react';
import {useTranslation} from 'react-i18next';
import type {PdfIRBlock} from '@/modules/knowledge/api/pdfIR';
import type {FormulaEvidence} from '@/modules/knowledge/api/formulaExplanation';
import {layoutTable,tableCellHeaders,type PlacedTableCell} from './pdfTableGeometry';
import {tableDataCandidates} from './pdfTableAssociations';
import './PdfFormulaStudy.css';
import './PdfTableStudy.css';
interface Props {
 sourceKey:string;blocks:PdfIRBlock[];allBlocks:PdfIRBlock[];selected?:PdfIRBlock;
 onSelect:(block:PdfIRBlock)=>void;onNavigate:(evidence:FormulaEvidence)=>void;
 onReturn:()=>void;canReturn:boolean;onClose:()=>void;
}
export function PdfTableStudy({sourceKey,blocks,allBlocks,selected,onSelect,onNavigate,onReturn,canReturn,onClose}:Props){
 const {t}=useTranslation(),panel=useRef<HTMLElement>(null),cellButtons=useRef(new Map<string,HTMLButtonElement>());
 const selectedSection=useRef<HTMLElement>(null);
 const scope=`${sourceKey}/${selected?.id || ''}`;
 const hasSelected=!!selected;
 useEffect(()=>{
  if(!hasSelected)return;
  const frame=window.requestAnimationFrame(()=>{
   const container=panel.current,section=selectedSection.current;if(!container||!section)return;
   const headerHeight=container.querySelector('header')?.getBoundingClientRect().height||0;
   const top=section.getBoundingClientRect().top-container.getBoundingClientRect().top+container.scrollTop-headerHeight;
   container.scrollTo?.({top:Math.max(0,top),behavior:'auto'});
  });
  return()=>window.cancelAnimationFrame(frame);
 },[scope,hasSelected]);
 const [selection,setSelection]=useState<{scope:string;key:string}>();
 const [focus,setFocus]=useState<{scope:string;key:string}>();
 useEffect(()=>{panel.current?.focus({preventScroll:true});},[]);
 const rows=useMemo(()=>selected?.table?layoutTable(selected.table):[],[selected?.table]);
 const cells=useMemo(()=>rows.flat(),[rows]);
 const active=selection?.scope===scope?cells.find(c=>c.key===selection.key):undefined;
 const focusedKey=focus?.scope===scope?focus.key:cells[0]?.key;
 const headers=active?tableCellHeaders(rows,active):undefined;
 let topHeaderRows=0;while(topHeaderRows<rows.length&&rows[topHeaderRows].length>0&&rows[topHeaderRows].every(c=>c.cell.header))topHeaderRows++;
 const references=useMemo(()=>{
  const byId=new Map(allBlocks.map(block=>[block.id,block])),seen=new Set<string>();
  return (selected?.table?.related_blocks || []).flatMap(reference=>{
   const block=byId.get(reference.block_id);
   if(!block||seen.has(block.id)||!block.text?.trim()||block.page<0)return [];
   seen.add(block.id);return [{block,relation:reference.relation}];
  });
 },[selected?.table,allBlocks]);
 const candidates=useMemo(()=>selected?tableDataCandidates(selected,allBlocks):[],[selected,allBlocks]);
 const label=(block:PdfIRBlock)=>block.table?.number?t('pdfTable.numbered',{number:block.table.number,defaultValue:'Table {{number}}'}):t('pdfTable.unnumbered',{defaultValue:'Unnumbered table'});
 const navigate=(block:PdfIRBlock)=>onNavigate({id:block.id,text:block.text||block.table?.captions.join('\n')||'',page:block.page,bbox:block.bbox});
 const move=(event:React.KeyboardEvent,cell:PlacedTableCell)=>{
  let next:PlacedTableCell|undefined;
  const sameRow=cells.filter(c=>c.row<=cell.row&&c.row+c.rowSpan>cell.row);
  if(event.key==='ArrowRight')next=sameRow.filter(c=>c.column>=cell.column+cell.colSpan).sort((a,b)=>a.column-b.column)[0];
  else if(event.key==='ArrowLeft')next=sameRow.filter(c=>c.column+c.colSpan<=cell.column).sort((a,b)=>b.column-a.column)[0];
  else if(event.key==='ArrowDown')next=cells.filter(c=>c.row>=cell.row+cell.rowSpan&&c.column<=cell.column&&c.column+c.colSpan>cell.column).sort((a,b)=>a.row-b.row)[0];
  else if(event.key==='ArrowUp')next=cells.filter(c=>c.row+c.rowSpan<=cell.row&&c.column<=cell.column&&c.column+c.colSpan>cell.column).sort((a,b)=>b.row-a.row)[0];
  else if(event.key==='Home')next=sameRow[0];
  else if(event.key==='End')next=sameRow[sameRow.length-1];
  else return;
  event.preventDefault();event.stopPropagation();
  if(next){setFocus({scope,key:next.key});cellButtons.current.get(next.key)?.focus();}
 };
 return <aside ref={panel} tabIndex={-1} className="pdf-formula-study pdf-table-study" aria-label={t('pdfTable.study',{defaultValue:'Table reader'})} onMouseUp={event=>event.stopPropagation()} onKeyDown={event=>{if(event.key==='Escape'){event.stopPropagation();onClose();}}}>
  <header><div><span className="pdf-study-eyebrow">{t('pdfTable.eyebrow',{defaultValue:'Original evidence'})}</span><h2>{t('pdfTable.study',{defaultValue:'Table reader'})}</h2></div><button className="pdf-study-close" aria-label={t('pdfTable.close',{defaultValue:'Close table reader'})} onClick={onClose}>×</button></header>
  <div className="pdf-study-navigation"><label>{t('pdfTable.catalog',{defaultValue:'Table catalog'})}<select aria-label={t('pdfTable.catalog',{defaultValue:'Table catalog'})} value={selected?.id||''} onChange={event=>{const block=blocks.find(b=>b.id===event.target.value);if(block)onSelect(block);}}><option value="" disabled>{t('pdfTable.choose',{defaultValue:'Choose a table'})}</option>{blocks.map(block=>{
   const index=blocks.filter(b=>b.page===block.page).findIndex(b=>b.id===block.id)+1;
   return <option key={block.id} value={block.id}>{label(block)} · {t('pdfTable.catalogLocation',{page:block.page+1,index,defaultValue:'p. {{page}} · item {{index}}'})}</option>;
  })}</select></label>{canReturn&&<button onClick={onReturn}>{t('pdfFormula.returnReading')}</button>}</div>
  {!selected?<div className="pdf-study-empty"><strong>{t('pdfTable.choose',{defaultValue:'Choose a table'})}</strong><p>{t('pdfTable.hint',{defaultValue:'Explore cells and the paper’s explicit references to this table.'})}</p></div>:<div className="pdf-study-body">
   <section ref={selectedSection}><div className="pdf-table-title"><h3>{label(selected)}</h3><button onClick={()=>navigate(selected)}>{t('pdfTable.locate',{defaultValue:'Locate original table'})}</button></div>
    {(selected.table?.captions||[]).map((caption,index)=><p className="pdf-table-caption" key={index}>{caption}</p>)}
    <p className="pdf-study-section-label">{t('pdfTable.selectHint',{defaultValue:'Select a cell to inspect it. Use arrow keys to move between cells, then Enter to select.'})}</p>
    {rows.some(row=>row.length)?<div key={scope} className="pdf-table-scroll" role="region" aria-label={t('pdfTable.data',{defaultValue:'Table data'})} tabIndex={0}><table aria-label={label(selected)}><tbody>{rows.map((row,rowIndex)=><tr key={rowIndex}>{row.map(placed=>{
     const Tag=placed.cell.header?'th':'td';
     return <Tag key={placed.key} rowSpan={placed.rowSpan} colSpan={placed.colSpan} scope={placed.cell.header&&rowIndex<topHeaderRows?'col':undefined} className={active?.key===placed.key?'is-selected':''}><button ref={node=>{if(node)cellButtons.current.set(placed.key,node);else cellButtons.current.delete(placed.key);}} type="button" tabIndex={focusedKey===placed.key?0:-1} aria-pressed={active?.key===placed.key} aria-label={placed.cell.text?undefined:t('pdfTable.emptyCell',{defaultValue:'Empty cell'})} onFocus={()=>setFocus({scope,key:placed.key})} onKeyDown={event=>move(event,placed)} onClick={()=>{if(!window.getSelection()?.toString()){setSelection({scope,key:placed.key});setFocus({scope,key:placed.key});}}}>{placed.cell.text||'—'}</button></Tag>;
    })}</tr>)}</tbody></table></div>:<p className="pdf-table-unavailable">{t('pdfTable.unavailable',{defaultValue:'Structured cells are unavailable. Read this table in the original PDF.'})}</p>}
    {active&&<div className="pdf-table-cell-detail" role="region" aria-label={t('pdfTable.selectedCell',{defaultValue:'Selected cell'})} aria-live="polite"><h4>{t('pdfTable.selectedCell',{defaultValue:'Selected cell'})}</h4><div className="pdf-study-section-label">{t('pdfTable.cellPosition',{row:active.row+1,column:active.column+1,defaultValue:'Row {{row}} · column {{column}}'})}{(active.rowSpan>1||active.colSpan>1)&&` · ${t('pdfTable.cellSpan',{rows:active.rowSpan,columns:active.colSpan,defaultValue:'spans {{rows}} rows × {{columns}} columns'})}`}</div><pre>{active.cell.text||t('pdfTable.emptyCell',{defaultValue:'Empty cell'})}</pre>{!!(headers?.rows.length||headers?.columns.length)&&<dl>{!!headers?.rows.length&&<><dt>{t('pdfTable.rowHeaders',{defaultValue:'Row headers'})}</dt><dd>{headers.rows.join(' / ')}</dd></>}{!!headers?.columns.length&&<><dt>{t('pdfTable.columnHeaders',{defaultValue:'Column headers'})}</dt><dd>{headers.columns.join(' / ')}</dd></>}</dl>}</div>}
    {!!selected.table?.footnotes.length&&<div className="pdf-table-footnotes"><h4>{t('pdfTable.footnotes',{defaultValue:'Table notes'})}</h4>{selected.table.footnotes.map((note,index)=><p key={index}>{note}</p>)}</div>}
   </section>
   <section className="pdf-study-evidence"><h3>{t('pdfTable.references',{defaultValue:'In the paper'})}</h3><p className="pdf-study-section-label">{t('pdfTable.referenceNote',{defaultValue:'Only explicit references matched to this table are shown. Quotes preserve the extracted original text.'})}</p>{references.length?references.map(({block,relation})=><article key={block.id}><div className="pdf-table-reference-heading"><span>{relation==='caption'?t('pdfTable.caption',{defaultValue:'Caption'}):t('pdfTable.mention',{defaultValue:'Text reference'})}</span><button className="pdf-study-citation" onClick={()=>navigate(block)}>{t('pdfFormula.sourcePage',{page:block.page+1})}</button></div><blockquote>{block.text}</blockquote>{!block.bbox&&<small>{t('pdfFormula.pageOnly')}</small>}</article>):<p>{t('pdfTable.noReferences',{defaultValue:'No unambiguous source references were identified. The caption above may still describe this table.'})}</p>}</section>
   {!!candidates.length&&<section className="pdf-study-evidence pdf-table-data-clues"><h3>{t('pdfTable.dataClues',{defaultValue:'Data matches · association unverified'})}</h3><p className="pdf-study-section-label">{t('pdfTable.dataClueNote',{defaultValue:'These paragraphs share at least three distinct decimal values with this table, more than any other table. This suggests a possible connection, not a verified reference.'})}</p>{candidates.map(({block,matchedValues})=><article key={block.id}><div className="pdf-table-reference-heading"><span className="pdf-table-clue-badge">{t('pdfTable.dataClueBadge',{defaultValue:'Unverified association'})}</span><button className="pdf-study-citation" onClick={()=>navigate(block)}>{t('pdfFormula.sourcePage',{page:block.page+1})}</button></div><blockquote>{block.text}</blockquote><p className="pdf-study-section-label">{t('pdfTable.matchedValues',{values:matchedValues.join(', '),defaultValue:'Matched values: {{values}}'})}</p>{!block.bbox&&<small>{t('pdfFormula.pageOnly')}</small>}</article>)}</section>}
   {!!selected.table?.warnings?.length&&<section className="pdf-study-warnings"><h3>{t('pdfFormula.limitations')}</h3><ul>{selected.table.warnings.map((warning,index)=><li key={index}>{warning}</li>)}</ul></section>}
   <p className="pdf-study-disclaimer">{t('pdfTable.extractionNote',{defaultValue:'Cells are extracted from the PDF and may contain recognition errors. Verify important values in the original table.'})}</p>
  </div>}
 </aside>;
}
