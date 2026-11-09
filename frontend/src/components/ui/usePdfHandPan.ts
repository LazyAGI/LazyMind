import {useEffect,useLayoutEffect,useRef,useState,type PointerEvent,type RefObject} from 'react';

interface Gesture {pointerId:number;x:number;y:number;left:number;top:number}

/** Move the real PDF viewport so canvas, text, and annotations retain one coordinate system. */
export function usePdfHandPan(viewport:RefObject<HTMLDivElement>,source:unknown){
  const [active,setActive]=useState(false);
  const [dragging,setDragging]=useState(false);
  // A fitted page has no free space at its edges. These gutters make dragging
  // possible in both directions even when the first page is at the top.
  const [gutter,setGutter]=useState(0);
  const [gutterY,setGutterY]=useState(0);
  const appliedGutter=useRef(0);
  const appliedGutterY=useRef(0);
  const gesture=useRef<Gesture>();

  useEffect(()=>{
    gesture.current=undefined;
    setActive(false);setDragging(false);setGutter(0);setGutterY(0);
  },[source]);

  useLayoutEffect(()=>{
    if(viewport.current){
      viewport.current.scrollLeft+=gutter-appliedGutter.current;
      viewport.current.scrollTop+=gutterY-appliedGutterY.current;
    }
    appliedGutter.current=gutter;
    appliedGutterY.current=gutterY;
  },[gutter,gutterY,viewport]);

  const disable=()=>{gesture.current=undefined;setDragging(false);setActive(false);};
  const toggle=()=>{
    if(active){disable();return;}
    setGutter(current=>current||Math.max(1,viewport.current?.clientWidth||window.innerWidth));
    setGutterY(current=>current||Math.max(1,viewport.current?.clientHeight||window.innerHeight));
    setActive(true);
  };
  const onPointerDown=(event:PointerEvent<HTMLDivElement>)=>{
    if(!active||event.button!==0)return;
    const pane=viewport.current;if(!pane)return;
    gesture.current={pointerId:event.pointerId,x:event.clientX,y:event.clientY,left:pane.scrollLeft,top:pane.scrollTop};
    pane.setPointerCapture?.(event.pointerId);
    event.preventDefault();event.stopPropagation();
    setDragging(true);
  };
  const onPointerMove=(event:PointerEvent<HTMLDivElement>)=>{
    const start=gesture.current;
    if(!start||start.pointerId!==event.pointerId||!viewport.current)return;
    viewport.current.scrollLeft=start.left-(event.clientX-start.x);
    viewport.current.scrollTop=start.top-(event.clientY-start.y);
    event.preventDefault();
  };
  const onPointerEnd=(event:PointerEvent<HTMLDivElement>)=>{
    if(gesture.current?.pointerId!==event.pointerId)return;
    gesture.current=undefined;
    setDragging(false);
  };
  return {active,dragging,gutter,gutterY,toggle,disable,onPointerDown,onPointerMove,onPointerEnd};
}
