import {useEffect,useRef,type ReactNode} from 'react';
import {createPortal} from 'react-dom';
import './PdfStudyDialog.css';

interface Props {children:ReactNode;onClose:()=>void}

/** Keep source interpretation over the PDF instead of resizing its reading pane. */
export function PdfStudyDialog({children,onClose}:Props){
 const panel=useRef<HTMLDivElement>(null);
 const previousFocus=useRef<HTMLElement|null>(null);
 useEffect(()=>{
  previousFocus.current=window.document.activeElement instanceof HTMLElement?window.document.activeElement:null;
  panel.current?.focus({preventScroll:true});
  return()=>previousFocus.current?.focus({preventScroll:true});
 },[]);
 return createPortal(<div className="pdf-study-overlay" onPointerDown={event=>{if(event.target===event.currentTarget)onClose();}}>
  <div ref={panel} className="pdf-study-dialog" role="dialog" aria-modal="true" aria-label="原文解读" tabIndex={-1} onKeyDown={event=>{if(event.key==='Escape'){event.stopPropagation();onClose();}}}>
   <button className="pdf-study-dialog-close" type="button" aria-label="关闭原文解读" onClick={onClose}>×</button>
   {children}
  </div>
 </div>,window.document.body);
}
