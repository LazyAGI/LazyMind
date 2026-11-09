import {useEffect, useRef, useState} from 'react';
import {usePaperReaderLabels} from './paperReaderLabels';

/** Editing the displayed percentage must not reset the reader's current scale. */
export function PdfZoomControl({zoom, disabled, onChange}: {zoom:number; disabled:boolean; onChange:(zoom:number)=>void}) {
  const {label}=usePaperReaderLabels();
  const [draft,setDraft]=useState<string>();
  const editing=useRef(false);
  const button=useRef<HTMLButtonElement>(null);
  useEffect(()=>{if(disabled){editing.current=false;setDraft(undefined);}},[disabled]);
  const finish=(commit:boolean, restoreFocus=false)=>{
    if(!editing.current)return;
    editing.current=false;
    const percent=Number(draft);
    if(commit && draft?.trim() && Number.isFinite(percent))onChange(Math.min(200,Math.max(50,percent))/100);
    setDraft(undefined);
    if(restoreFocus)requestAnimationFrame(()=>button.current?.focus());
  };
  return draft===undefined
    ? <button ref={button} className="pdf-reader-zoom-value" type="button" disabled={disabled} title={label('zoomPercent')} onClick={()=>{editing.current=true;setDraft(String(Math.round(zoom*100)));}}>{Math.round(zoom*100)}%</button>
    : <label className="pdf-reader-zoom-editor"><input autoFocus type="number" min={50} max={200} step={1} aria-label={label('zoomPercent')} value={draft} onFocus={event=>event.currentTarget.select()} onChange={event=>setDraft(event.target.value)} onBlur={()=>finish(true)} onKeyDown={event=>{if(event.key==='Enter'||event.key==='Escape'){event.preventDefault();event.stopPropagation();finish(event.key==='Enter',true);}}}/><span aria-hidden="true">%</span></label>;
}
