import {useEffect,useId,useMemo,useRef,useState,type KeyboardEvent} from 'react';
import {useTranslation} from 'react-i18next';
import type {PdfIRDocument} from '@/modules/knowledge/api/pdfIR';
import {paperOutline} from './PaperReaderSidebar';
import './PaperOutlineRail.scss';

export interface PaperOutlineRailProps {
 document?:PdfIRDocument;
 /** One-based page number, matching the PDF reader. */
 currentPage:number;
 onNavigate:(blockId:string)=>void;
 disabled?:boolean;
}

export function PaperOutlineRail({document:source,currentPage,onNavigate,disabled=false}:PaperOutlineRailProps){
 const {t}=useTranslation();
 const id=useId(),root=useRef<HTMLDivElement>(null),trigger=useRef<HTMLButtonElement>(null);
 const suppressFocus=useRef(false);
 const [open,setOpen]=useState(false);
 const [selectedId,setSelectedId]=useState<string>();
 const outline=useMemo(()=>paperOutline(source).filter(block=>Number.isFinite(block.page)).sort((a,b)=>a.page-b.page),[source]);
 // Page information cannot distinguish headings on the same page; source order is the stable tie-breaker.
 const preceding=outline.filter(block=>block.page<currentPage);
 const selected=outline.find(block=>block.id===selectedId);
 const active=selected?.page===currentPage-1?selected.id:preceding[preceding.length-1]?.id;
 useEffect(()=>{setSelectedId(id=>outline.find(block=>block.id===id)?.page===currentPage-1?id:undefined);},[currentPage,outline]);
 const title=t('paperShell.outline',{defaultValue:'Outline'});
 const empty=source?t('paperShell.noOutline',{defaultValue:'No section headings were identified in this PDF.'}):t('paperShell.parseOutline',{defaultValue:'Parse the original PDF to see its outline.'});
 const close=(restoreFocus=false)=>{
  setOpen(false);
  if(restoreFocus){suppressFocus.current=true;trigger.current?.focus();suppressFocus.current=false;}
 };
 useEffect(()=>{
  if(!open)return;
  const outside=(event:PointerEvent)=>{if(!root.current?.contains(event.target as Node))setOpen(false);};
  window.document.addEventListener('pointerdown',outside);
  return ()=>window.document.removeEventListener('pointerdown',outside);
 },[open]);
 useEffect(()=>{setOpen(false);},[source,disabled]);
 useEffect(()=>{
  if(!open||disabled)return;
  const chapter=root.current?.querySelector<HTMLButtonElement>('.paper-outline-rail__chapter[aria-current="location"]');
  chapter?.scrollIntoView?.({block:'nearest',inline:'nearest'});
 },[open,active,disabled]);
 const onKeyDown=(event:KeyboardEvent<HTMLDivElement>)=>{
  if(event.key==='Escape'){event.preventDefault();event.stopPropagation();close(true);return;}
  if(!['ArrowDown','ArrowUp','Home','End'].includes(event.key)||disabled)return;
  event.preventDefault();
  setOpen(true);
  // The popover may be mounting in response to this key press.
  requestAnimationFrame(()=>{
   const buttons=Array.from(root.current?.querySelectorAll<HTMLButtonElement>('.paper-outline-rail__chapter')||[]);
   const index=buttons.indexOf(window.document.activeElement as HTMLButtonElement);
   const current=buttons.findIndex(button=>button.getAttribute('aria-current')==='location');
   const next=event.key==='Home'?0:event.key==='End'?buttons.length-1:index<0?Math.max(0,current):(index+(event.key==='ArrowDown'?1:-1)+buttons.length)%buttons.length;
   buttons[next]?.focus();
  });
 };
 return <div ref={root} className="paper-outline-rail" onKeyDown={onKeyDown}
  onMouseLeave={()=>{if(!root.current?.contains(window.document.activeElement))close();}}
  onBlur={event=>{if(!event.currentTarget.contains(event.relatedTarget as Node))close();}}>
  <button ref={trigger} type="button" className="paper-outline-rail__trigger" aria-label={title} title={outline.length?title:empty}
   aria-expanded={open&&!disabled} aria-controls={`${id}-outline`} disabled={disabled}
   onMouseEnter={()=>{if(!disabled)setOpen(true);}} onFocus={()=>{if(!suppressFocus.current&&!disabled)setOpen(true);}}
   onClick={()=>setOpen(true)}>
   <span className="paper-outline-rail__ticks" aria-hidden="true">{outline.length?outline.map(block=><span key={block.id} className={block.id===active?'is-active':undefined}/>):<span className="is-empty"/>}</span>
  </button>
  <nav id={`${id}-outline`} className={`paper-outline-rail__popover${open&&!disabled?" is-open":""}`} aria-hidden={!open||disabled} aria-label={title}>
   <div className="paper-outline-rail__title">{title}</div>
   {outline.length?<ol>{outline.map(block=>{
    const numbering=block.text?.match(/^(\d+(?:\.\d+)*)\.?\s/);
    const depth=Math.min(3,numbering?numbering[1].split('.').length-1:0);
    return <li key={block.id}><button type="button" className="paper-outline-rail__chapter" style={{paddingInlineStart:12+depth*14}} aria-current={block.id===active?'location':undefined}
     onClick={()=>{setSelectedId(block.id);onNavigate(block.id);}}><span>{block.text}</span></button></li>;
   })}</ol>:<p className="paper-outline-rail__empty">{empty}</p>}
  </nav>
 </div>;
}
