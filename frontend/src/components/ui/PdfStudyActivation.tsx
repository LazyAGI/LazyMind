import {useLayoutEffect,useState,type RefObject} from 'react';
import {createPortal} from 'react-dom';
import './PdfStudyActivation.css';

/** A one-shot acknowledgement; it never represents model generation progress. */
export function PdfStudyActivation({frame}:{frame:RefObject<HTMLElement>}){
 const [bounds,setBounds]=useState<DOMRect>();
 const [active,setActive]=useState(true);
 useLayoutEffect(()=>{
  if(!active)return;
  const element=frame.current;if(!element)return;
  const update=()=>setBounds(element.getBoundingClientRect());
  update();const observer=new ResizeObserver(update);observer.observe(element);
  window.addEventListener('scroll',update,true);window.addEventListener('resize',update);
  const timer=window.setTimeout(()=>setActive(false),2200);
  return()=>{observer.disconnect();window.clearTimeout(timer);window.removeEventListener('scroll',update,true);window.removeEventListener('resize',update);};
 },[frame,active]);
 if(!active||!bounds||bounds.width<=0||bounds.height<=0)return null;
 return createPortal(<div className="pdf-study-activation" aria-hidden="true" onAnimationEnd={()=>setActive(false)} style={{left:bounds.left,top:bounds.top,width:bounds.width,height:bounds.height}}/>,document.body);
}
export function StudySparkle(){return <svg aria-hidden="true" viewBox="0 0 24 24" width="20" height="20" fill="none"><path d="m11 3 2.6 6.4L20 12l-6.4 2.6L11 21l-2.6-6.4L2 12l6.4-2.6L11 3Z" fill="currentColor"/><path d="m20 1 .9 2.1L23 4l-2.1.9L20 7l-.9-2.1L17 4l2.1-.9L20 1Z" fill="currentColor"/></svg>;}
