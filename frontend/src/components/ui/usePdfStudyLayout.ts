import {useEffect, useState, type RefObject} from 'react';

// Observe the fixed outer reader, not the PDF pane whose width changes when
// the study panel opens. This avoids a split/stack resize feedback loop.
export function usePdfStudyLayout(reader:RefObject<HTMLElement>) {
  const [layout,setLayout]=useState<'stacked'|'split'>('stacked');
  useEffect(()=>{
    const element=reader.current;
    if(!element)return;
    const update=()=>{if(element.clientWidth>0)setLayout(element.clientWidth<920?'stacked':'split');};
    update();
    const observer=typeof ResizeObserver==='undefined'?undefined:new ResizeObserver(update);
    observer?.observe(element);
    window.addEventListener('resize',update);
    return()=>{observer?.disconnect();window.removeEventListener('resize',update);};
  },[reader]);
  return layout;
}
