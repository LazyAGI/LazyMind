import {useEffect,useMemo,useRef,useState} from 'react';
import {useTranslation} from 'react-i18next';
import type {PdfIRBlock} from '@/modules/knowledge/api/pdfIR';
import './PdfTableLayer.css';
export function hasTableGeometry(block:PdfIRBlock):boolean {
 const b=block.bbox;
 return block.type==='table'&&!!block.table&&!!b&&b.length===4&&b.every(v=>Number.isFinite(v)&&v>=0&&v<=1)&&b[0]<b[2]&&b[1]<b[3];
}
export function PdfTableLayer({blocks,onSelect}:{blocks:PdfIRBlock[];onSelect:(block:PdfIRBlock)=>void}){
 const {t}=useTranslation(),root=useRef<HTMLDivElement>(null);
 const tables=useMemo(()=>blocks.filter(hasTableGeometry),[blocks]);
 const [hovered,setHovered]=useState<string>();
 const selectCurrent=useRef(onSelect);selectCurrent.current=onSelect;
 useEffect(()=>{
  const page=root.current?.parentElement;if(!page)return;
  let down:{x:number;y:number;id:string;dragged:boolean}|undefined;
  let pending:string|undefined;
  const hit=(x:number,y:number)=>{
   const rect=page.getBoundingClientRect();if(rect.width<=0||rect.height<=0)return;
   const nx=(x-rect.left)/rect.width,ny=(y-rect.top)/rect.height;
   return tables.find(b=>nx>=b.bbox![0]&&nx<=b.bbox![2]&&ny>=b.bbox![1]&&ny<=b.bbox![3]);
  };
  const blocked=(event:MouseEvent)=>event.button!==0||event.altKey||event.ctrlKey||event.metaKey||event.shiftKey||!!window.getSelection()?.toString()||(event.target instanceof Element&&!!event.target.closest('a,button,input,textarea,select,[role="button"],[contenteditable="true"]'));
  const clear=()=>setHovered(undefined);
  const leave=()=>{down=undefined;pending=undefined;clear();};
  const start=(event:PointerEvent)=>{
   clear();down=undefined;pending=undefined;if(blocked(event))return;
   const block=hit(event.clientX,event.clientY);if(block)down={x:event.clientX,y:event.clientY,id:block.id,dragged:false};
  };
  const move=(event:PointerEvent)=>{
   if(down&&Math.hypot(event.clientX-down.x,event.clientY-down.y)>5)down.dragged=true;
   if(event.buttons||window.getSelection()?.toString()){clear();return;}
   setHovered(hit(event.clientX,event.clientY)?.id);
  };
  const end=(event:PointerEvent)=>{
   pending=undefined;
   if(down&&!down.dragged&&!blocked(event)&&Math.hypot(event.clientX-down.x,event.clientY-down.y)<=5&&hit(event.clientX,event.clientY)?.id===down.id)pending=down.id;
   down=undefined;
  };
  const click=(event:MouseEvent)=>{
   const id=pending;pending=undefined;if(!id||blocked(event))return;
   const block=hit(event.clientX,event.clientY);if(block?.id===id)selectCurrent.current(block);
  };
  const selection=()=>{if(window.getSelection()?.toString())leave();};
  page.addEventListener('pointermove',move);page.addEventListener('pointerleave',leave);page.addEventListener('pointerdown',start);page.addEventListener('pointerup',end);page.addEventListener('pointercancel',leave);page.addEventListener('click',click);document.addEventListener('selectionchange',selection);
  return()=>{page.removeEventListener('pointermove',move);page.removeEventListener('pointerleave',leave);page.removeEventListener('pointerdown',start);page.removeEventListener('pointerup',end);page.removeEventListener('pointercancel',leave);page.removeEventListener('click',click);document.removeEventListener('selectionchange',selection);};
 },[tables]);
 return <div ref={root} className="pdf-table-layer">{tables.map((block,index)=><div key={block.id} className={`pdf-table-region${hovered===block.id?' is-hovered':''}`} style={{left:`${block.bbox![0]*100}%`,top:`${block.bbox![1]*100}%`,width:`${(block.bbox![2]-block.bbox![0])*100}%`,height:`${(block.bbox![3]-block.bbox![1])*100}%`}}><button className="pdf-table-marker" aria-label={`${t('pdfTable.open',{defaultValue:'Explore table'})} · ${block.table?.number?t('pdfTable.numbered',{number:block.table.number,defaultValue:'Table {{number}}'}):t('pdfTable.unnumbered',{defaultValue:'Unnumbered table'})} · ${t('pdfTable.catalogLocation',{page:block.page+1,index:index+1,defaultValue:'p. {{page}} · item {{index}}'})}`} title={t('pdfTable.open',{defaultValue:'Explore table'})} onMouseDown={event=>event.preventDefault()} onMouseUp={event=>event.stopPropagation()} onClick={event=>{event.stopPropagation();onSelect(block);}}><svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true"><rect x="1.5" y="2" width="13" height="12" rx="1.5" fill="none" stroke="currentColor"/><path d="M2 6h12M2 10h12M6 2v12M10 6v8" stroke="currentColor" fill="none"/></svg><span>{t('pdfTable.explore',{defaultValue:'Explore'})}</span></button></div>)}</div>;
}
