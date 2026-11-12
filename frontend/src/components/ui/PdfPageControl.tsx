import {useEffect,useRef,useState} from 'react';
import {usePaperReaderLabels} from './paperReaderLabels';

/** Keep page navigation independent of each digit typed in the editor. */
export function PdfPageControl({page,total,disabled,onChange}: {page:number;total:number;disabled:boolean;onChange:(page:number)=>void}) {
  const {label}=usePaperReaderLabels();
  const [draft,setDraft]=useState<string>();
  const editing=useRef(false);
  const button=useRef<HTMLButtonElement>(null);
  useEffect(()=>{if(disabled||total<1){editing.current=false;setDraft(undefined);}},[disabled,total]);
  const finish=(commit:boolean,restoreFocus=false)=>{
    if(!editing.current)return;
    editing.current=false;
    const requested=Number(draft);
    if(commit && draft?.trim() && Number.isFinite(requested) && total>0)onChange(Math.min(total,Math.max(1,Math.round(requested))));
    setDraft(undefined);
    if(restoreFocus)requestAnimationFrame(()=>button.current?.focus());
  };
  return draft===undefined
    ? <button ref={button} className="pdf-reader-page-value" type="button" disabled={disabled||total<1} title={label('page')} aria-label={`${label('page')} ${page} / ${total||'—'}`} onClick={()=>{editing.current=true;setDraft(String(page));}}><strong>{disabled?'—':page}</strong><span className="pdf-reader-page-total">/ {total||'—'}</span></button>
    : <label className="pdf-reader-page-editor"><input autoFocus className="pdf-reader-page-input" type="number" inputMode="numeric" min={1} max={total} step={1} aria-label={label('page')} value={draft} onFocus={event=>event.currentTarget.select()} onChange={event=>setDraft(event.target.value)} onBlur={()=>finish(true)} onKeyDown={event=>{if(event.key==='Enter'||event.key==='Escape'){event.preventDefault();event.stopPropagation();finish(event.key==='Enter',true);}}}/><span className="pdf-reader-page-total">/ {total}</span></label>;
}
