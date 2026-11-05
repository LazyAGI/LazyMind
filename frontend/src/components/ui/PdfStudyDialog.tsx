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
  const background=Array.from(window.document.body.children).filter(element=>!element.contains(panel.current)&&!element.hasAttribute('inert'));
  background.forEach(element=>element.setAttribute('inert',''));
  return()=>{background.forEach(element=>element.removeAttribute('inert'));previousFocus.current?.focus({preventScroll:true});};
 },[]);
 return createPortal(<div className="pdf-study-overlay" onPointerDown={event=>{if(event.target===event.currentTarget)onClose();}}>
  <div ref={panel} className="pdf-study-dialog" role="dialog" aria-modal="true" aria-label="原文解读" tabIndex={-1} onKeyDown={event=>{
   if(event.key==='Escape'){event.stopPropagation();onClose();return;}
   if(event.key!=='Tab')return;
   const controls=Array.from(event.currentTarget.querySelectorAll<HTMLElement>('button,a[href],input,select,textarea,summary,[tabindex]')).filter(element=>{
    if(element.tabIndex<0||element.matches(':disabled'))return false;
    for(let ancestor:HTMLElement|null=element;ancestor&&ancestor!==panel.current;ancestor=ancestor.parentElement){
     if(ancestor.hidden||ancestor.hasAttribute('inert')||getComputedStyle(ancestor).display==='none'||getComputedStyle(ancestor).visibility==='hidden')return false;
     if(ancestor instanceof HTMLDetailsElement&&!ancestor.open&&!ancestor.querySelector(':scope > summary')?.contains(element))return false;
    }
    return true;
   });
   const first=controls[0],last=controls[controls.length-1],focused=window.document.activeElement;
   if(!first){event.preventDefault();panel.current?.focus();}
   else if(event.shiftKey&&(focused===first||focused===panel.current)){event.preventDefault();last.focus();}
   else if(!event.shiftKey&&(focused===last||focused===panel.current)){event.preventDefault();first.focus();}
  }}>
   <div className="pdf-study-dialog-actions"><button className="pdf-study-dialog-close" type="button" aria-label="关闭原文解读" onClick={onClose}>×</button></div>
   {children}
  </div>
 </div>,window.document.body);
}
