import {useEffect,useRef,useState,type ReactNode} from 'react';
import {createPortal} from 'react-dom';
import './PdfStudyDialog.css';

export interface PinnedGeometry {x:number;y:number;width:number;height:number}
type ResizeEdge='n'|'ne'|'e'|'se'|'s'|'sw'|'w'|'nw';
interface Props {children:ReactNode;onClose:()=>void;onPinChange?:(pinned:boolean,geometry?:PinnedGeometry)=>boolean|void;onMinimize?:(geometry:PinnedGeometry)=>void;onGeometryChange?:(geometry:PinnedGeometry)=>void;initialPinned?:boolean;initialGeometry?:PinnedGeometry}
const clamp=(value:number,min:number,max:number)=>Math.min(Math.max(value,min),Math.max(min,max));
const resizeEdges:ResizeEdge[]=['n','ne','e','se','s','sw','w','nw'];
function edgeRect(edge:ResizeEdge,box:PinnedGeometry){
 const corner=edge.length===2;
 const width=corner?14:edge==='n'||edge==='s'?Math.max(1,box.width-28):8;
 const height=corner?14:edge==='e'||edge==='w'?Math.max(1,box.height-28):8;
 const left=edge.includes('w')?box.x-7:edge.includes('e')?box.x+box.width-7:box.x+14;
 const top=edge.includes('n')?box.y-7:edge.includes('s')?box.y+box.height-7:box.y+14;
 return {left,top,width,height};
}

/** Keep source interpretation over the PDF instead of resizing its reading pane. */
export function PdfStudyDialog({children,onClose,onPinChange,onMinimize,onGeometryChange,initialPinned=false,initialGeometry}:Props){
 const panel=useRef<HTMLDivElement>(null);
 const previousFocus=useRef<HTMLElement|null>(null);
 const drag=useRef<{pointerId:number;startX:number;startY:number;x:number;y:number;width:number;height:number}>();
 const resizeDrag=useRef<{pointerId:number;startX:number;startY:number;geometry:PinnedGeometry;edge:ResizeEdge}>();
 const onGeometryChangeRef=useRef(onGeometryChange);
 onGeometryChangeRef.current=onGeometryChange;
 const [pinned,setPinned]=useState(initialPinned);
 const [geometry,setGeometry]=useState<PinnedGeometry|undefined>(initialGeometry);
 useEffect(()=>{
  previousFocus.current=window.document.activeElement instanceof HTMLElement?window.document.activeElement:null;
  return()=>previousFocus.current?.focus({preventScroll:true});
 },[]);
 useEffect(()=>{
  if(pinned)return;
  panel.current?.focus({preventScroll:true});
  const background=Array.from(window.document.body.children).filter(element=>!element.contains(panel.current)&&!element.hasAttribute('inert'));
  background.forEach(element=>element.setAttribute('inert',''));
  return()=>background.forEach(element=>element.removeAttribute('inert'));
 },[pinned]);
 useEffect(()=>{
  if(!pinned||!panel.current||typeof ResizeObserver==='undefined')return;
  const observer=new ResizeObserver(()=>{
   const bounds=panel.current?.getBoundingClientRect();
   if(!bounds||!bounds.width||!bounds.height)return;
   setGeometry(current=>current&&(
    Math.abs(current.width-bounds.width)>1||Math.abs(current.height-bounds.height)>1
   )?{...current,width:bounds.width,height:bounds.height,x:clamp(current.x,8,window.innerWidth-bounds.width-8),y:clamp(current.y,8,window.innerHeight-bounds.height-8)}:current);
  });
  observer.observe(panel.current);
  return()=>observer.disconnect();
 },[pinned]);
 useEffect(()=>{
  if(!pinned)return;
  const keepVisible=()=>setGeometry(current=>current?{...current,x:clamp(current.x,8,window.innerWidth-current.width-8),y:clamp(current.y,8,window.innerHeight-current.height-8)}:current);
  window.addEventListener('resize',keepVisible);
  return()=>window.removeEventListener('resize',keepVisible);
 },[pinned]);
 useEffect(()=>{
  if(!pinned||!geometry)return;
  const timer=window.setTimeout(()=>onGeometryChangeRef.current?.(geometry),250);
  return()=>window.clearTimeout(timer);
 },[pinned,geometry]);
 const togglePinned=()=>{
  let nextGeometry=geometry;
  if(!pinned){
   const wide=!!panel.current?.querySelector('.pdf-table-study,.pdf-visual-view-switch');
   const width=Math.min(wide?720:560,Math.max(280,window.innerWidth-16));
   const height=Math.min(640,Math.max(180,Math.floor(window.innerHeight*.72)),Math.max(180,window.innerHeight-32));
   nextGeometry={x:Math.max(8,window.innerWidth-width-16),y:Math.min(72,Math.max(8,window.innerHeight-height-8)),width,height};
   setGeometry(nextGeometry);
  }
  drag.current=undefined;
  resizeDrag.current=undefined;
  if(onPinChange?.(!pinned,nextGeometry)===false)return;
  setPinned(!pinned);
 };
 return createPortal(<div className={`pdf-study-overlay${pinned?' is-pinned':''}`} onPointerDown={event=>{if(!pinned&&event.target===event.currentTarget)onClose();}}>
  <div ref={panel} className={`pdf-study-dialog${pinned?' is-pinned':''}`} role="dialog" aria-modal={pinned?undefined:'true'} aria-label="原文解读" tabIndex={-1} style={pinned&&geometry?{left:geometry.x,top:geometry.y,width:geometry.width,height:geometry.height}:undefined}
   onPointerDown={event=>{
    if(!pinned||event.button!==0||!(event.target instanceof Element))return;
    const header=event.target.closest('header');
    if(!header||header.parentElement?.parentElement!==panel.current||event.target.closest('button,a,input,textarea,select,summary'))return;
    const bounds=panel.current?.getBoundingClientRect();
    if(!bounds||!geometry)return;
    drag.current={pointerId:event.pointerId,startX:event.clientX,startY:event.clientY,x:bounds.width?bounds.x:geometry.x,y:bounds.height?bounds.y:geometry.y,width:bounds.width||geometry.width,height:bounds.height||geometry.height};
    event.currentTarget.setPointerCapture?.(event.pointerId);
    event.preventDefault();
   }}
   onPointerMove={event=>{
    const current=drag.current;if(!current||current.pointerId!==event.pointerId)return;
    setGeometry(geometry=>geometry?{...geometry,x:clamp(current.x+event.clientX-current.startX,8,window.innerWidth-current.width-8),y:clamp(current.y+event.clientY-current.startY,8,window.innerHeight-current.height-8)}:geometry);
   }}
   onPointerUp={event=>{if(drag.current?.pointerId===event.pointerId)drag.current=undefined;}}
   onPointerCancel={event=>{if(drag.current?.pointerId===event.pointerId)drag.current=undefined;}}
   onKeyDown={event=>{
   if(event.key==='Escape'){event.stopPropagation();onClose();return;}
   if(pinned&&event.altKey&&event.target instanceof Element&&!!event.target.closest('.pdf-study-dialog-pin')&&['ArrowLeft','ArrowRight','ArrowUp','ArrowDown'].includes(event.key)){
    event.preventDefault();
    const amount=event.shiftKey?24:12;
    setGeometry(current=>current?{...current,x:clamp(current.x+(event.key==='ArrowRight'?amount:event.key==='ArrowLeft'?-amount:0),8,window.innerWidth-current.width-8),y:clamp(current.y+(event.key==='ArrowDown'?amount:event.key==='ArrowUp'?-amount:0),8,window.innerHeight-current.height-8)}:current);
    return;
   }
   if(pinned)return;
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
   <div className="pdf-study-dialog-actions"><button className="pdf-study-dialog-pin" type="button" aria-label={pinned?'取消钉住研读窗口':'钉住研读窗口'} title={pinned?'取消钉住':'钉住窗口，拖动标题可移动'} aria-pressed={pinned} onClick={togglePinned}><svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M8 3h8l-1 5 3 3v2H6v-2l3-3-1-5Z"/><path d="M12 13v8"/></svg></button>{pinned&&onMinimize&&<button className="pdf-study-dialog-minimize" type="button" aria-label="最小化研读窗口" title="最小化到钉住区" onClick={()=>geometry&&onMinimize(geometry)}><svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" aria-hidden="true"><path d="M5 12h14"/></svg></button>}<button className="pdf-study-dialog-close" type="button" aria-label="关闭原文解读" onClick={onClose}>×</button></div>
   {children}
  </div>
  {pinned&&geometry&&resizeEdges.map(edge=><span key={edge} className={`pdf-study-resize-handle is-${edge}`} aria-hidden="true" style={edgeRect(edge,geometry)}
   onPointerDown={event=>{
    if(event.button!==0)return;
    resizeDrag.current={pointerId:event.pointerId,startX:event.clientX,startY:event.clientY,geometry,edge};
    event.currentTarget.setPointerCapture?.(event.pointerId);
    event.preventDefault();event.stopPropagation();
   }}
   onPointerMove={event=>{
    const start=resizeDrag.current;
    if(!start||start.pointerId!==event.pointerId)return;
    const {geometry:box,edge:side}=start;
    const dx=event.clientX-start.startX,dy=event.clientY-start.startY;
    const minWidth=Math.min(320,window.innerWidth-16),minHeight=Math.min(180,window.innerHeight-16);
    const left=side.includes('w')?clamp(box.x+dx,8,box.x+box.width-minWidth):box.x;
    const right=side.includes('e')?clamp(box.x+box.width+dx,box.x+minWidth,window.innerWidth-8):box.x+box.width;
    const top=side.includes('n')?clamp(box.y+dy,8,box.y+box.height-minHeight):box.y;
    const bottom=side.includes('s')?clamp(box.y+box.height+dy,box.y+minHeight,window.innerHeight-8):box.y+box.height;
    setGeometry({x:left,y:top,width:right-left,height:bottom-top});
    event.preventDefault();
   }}
   onPointerUp={event=>{if(resizeDrag.current?.pointerId===event.pointerId)resizeDrag.current=undefined;}}
   onPointerCancel={event=>{if(resizeDrag.current?.pointerId===event.pointerId)resizeDrag.current=undefined;}}
  />)}
 </div>,window.document.body);
}
