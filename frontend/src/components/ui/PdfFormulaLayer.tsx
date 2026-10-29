import { useTranslation } from "react-i18next";
import { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import katex from 'katex';
import 'katex/dist/katex.min.css';
import type { PdfIRBlock } from '@/modules/knowledge/api/pdfIR';
import './PdfFormulaLayer.css';

export function hasFormulaGeometry(block: PdfIRBlock): boolean {
  const box = block.bbox;
  return block.type === 'formula' && !!block.latex?.trim() && !!box && box.length === 4 && box.every(n=>Number.isFinite(n) && n>=0 && n<=1) && box[0]<box[2] && box[1]<box[3];
}
export function formulaAtPoint(blocks: PdfIRBlock[], x: number, y: number, rect: {left:number;top:number;width:number;height:number}) {
  if (rect.width<=0 || rect.height<=0) return;
  const nx=(x-rect.left)/rect.width, ny=(y-rect.top)/rect.height;
  return blocks.find(b=>hasFormulaGeometry(b) && nx>=b.bbox![0] && nx<=b.bbox![2] && ny>=b.bbox![1] && ny<=b.bbox![3]);
}
export function PdfFormulaLayer({blocks,onExplain}: {blocks:PdfIRBlock[];onExplain?:(block:PdfIRBlock)=>void}) {
  const { t } = useTranslation();
  const root=useRef<HTMLDivElement>(null);
  const card=useRef<HTMLDivElement>(null);
  const focusCard=useRef(false);
  const trigger=useRef<HTMLButtonElement | null>(null);
  const [active,setActive]=useState<PdfIRBlock>();
  const [position,setPosition]=useState({left:0,top:0});
  const [copyStatus,setCopyStatus]=useState('');
  const openTimer=useRef<ReturnType<typeof setTimeout>>();
  const closeTimer=useRef<ReturnType<typeof setTimeout>>();
  const candidates=useMemo(()=>blocks.filter(hasFormulaGeometry),[blocks]);
  const cancelClose=()=>clearTimeout(closeTimer.current);
  const close=()=>{clearTimeout(openTimer.current);cancelClose();setActive(undefined);};
  const scheduleClose=()=>{clearTimeout(openTimer.current);cancelClose();closeTimer.current=setTimeout(()=>setActive(undefined),250);};
  const open=(block:PdfIRBlock)=>{
    const rect=root.current?.parentElement?.getBoundingClientRect();
    if(!rect) return;
    const box=block.bbox!;
    setPosition({left:Math.max(8,Math.min(rect.left+box[0]*rect.width,window.innerWidth-376)),top:Math.max(8,Math.min(rect.top+box[3]*rect.height+8,window.innerHeight-320))});
    setCopyStatus('');setActive(block);
  };
  useEffect(()=>{
    if(active && focusCard.current){focusCard.current=false;card.current?.querySelector('button')?.focus();}
  },[active]);
  useEffect(()=>{
    setActive(undefined);
    const page=root.current?.parentElement;
    if(!page)return;
    let hovered:string|undefined;
    const move=(event:PointerEvent)=>{
      if((event.target as Element).closest('button')) return;
      if(event.buttons || window.getSelection()?.toString()){hovered=undefined;close();return;}
      const hit=formulaAtPoint(candidates,event.clientX,event.clientY,page.getBoundingClientRect());
      if(hit){cancelClose();if(hovered!==hit.id){clearTimeout(openTimer.current);hovered=hit.id;openTimer.current=setTimeout(()=>open(hit),300);}}
      else {hovered=undefined;scheduleClose();}
    };
    const down=(event:PointerEvent)=>{if(!(event.target as Element).closest('button')){hovered=undefined;close();}};
    const leave=()=>{hovered=undefined;scheduleClose();};
    const escape=(event:KeyboardEvent)=>{if(event.key==='Escape' && card.current){close();trigger.current?.focus();}};
    const selection=()=>{if(window.getSelection()?.toString() && !card.current?.contains(window.getSelection()?.anchorNode || null)){hovered=undefined;close();}};
    const scroll=(event:Event)=>{if(!(event.target instanceof Node) || !card.current?.contains(event.target))close();};
    page.addEventListener('pointermove',move);page.addEventListener('pointerdown',down);page.addEventListener('pointerleave',leave);
    document.addEventListener('keydown',escape);document.addEventListener('selectionchange',selection);
    window.addEventListener('scroll',scroll,true);window.addEventListener('resize',close);
    return()=>{clearTimeout(openTimer.current);cancelClose();page.removeEventListener('pointermove',move);page.removeEventListener('pointerdown',down);page.removeEventListener('pointerleave',leave);document.removeEventListener('keydown',escape);document.removeEventListener('selectionchange',selection);window.removeEventListener('scroll',scroll,true);window.removeEventListener('resize',close);};
  // Each page instance is keyed by the source revision; listeners only change with its blocks.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  },[candidates]);
  const preview=useMemo(()=>{
    if(!active)return null;
    try{return katex.renderToString(active.latex!,{throwOnError:true,displayMode:true,trust:false,strict:'ignore'});}catch{return null;}
  },[active]);
  return <div ref={root} className="pdf-formula-layer">
    {candidates.map((block,index)=><button key={block.id} className="pdf-formula-trigger" style={{left:`${Math.min(95,block.bbox![2]*100)}%`,top:`${block.bbox![1]*100}%`}} aria-label={t("pdfFormula.view", {number:index+1})} aria-haspopup="dialog" aria-expanded={active?.id===block.id} onClick={event=>{trigger.current=event.currentTarget;focusCard.current=true;cancelClose();clearTimeout(openTimer.current);open(block);}} onKeyDown={event=>{if(event.key==='ArrowDown'){event.preventDefault();card.current?.querySelector('button')?.focus();}}}>ƒ</button>)}
    {active && createPortal(<div ref={card} className="pdf-formula-card" role="dialog" aria-label={t("pdfFormula.title")} onMouseUp={event=>event.stopPropagation()} style={position} onPointerEnter={cancelClose} onPointerLeave={scheduleClose} onFocus={cancelClose} onBlur={event=>{if(!event.currentTarget.contains(event.relatedTarget as Node))scheduleClose();}}>
      <div className="pdf-formula-heading"><strong>{t("pdfFormula.formula")}</strong><button aria-label={t("pdfFormula.close")} onClick={()=>{close();trigger.current?.focus();}}>×</button></div>
      {preview ? <div className="pdf-formula-preview" dangerouslySetInnerHTML={{__html:preview}}/> : <p>{t("pdfFormula.previewError")}</p>}
      <label>{t("pdfFormula.raw")}<textarea readOnly value={active.latex} aria-label={t("pdfFormula.raw")} /></label>
      <div className="pdf-formula-footer">{onExplain&&<button onClick={()=>{onExplain(active);close();}}>{t("pdfFormula.explain")}</button>}<span role="status">{copyStatus}</span><button onClick={async()=>{try{await navigator.clipboard.writeText(active.latex!);setCopyStatus(t("pdfFormula.copied"));}catch{setCopyStatus(t("pdfFormula.copyError"));}}}>{t("pdfFormula.copy")}</button></div>
    </div>,document.body)}
  </div>;
}
