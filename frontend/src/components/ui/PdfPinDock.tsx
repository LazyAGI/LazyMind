import {createPortal} from 'react-dom';
import {useEffect,useRef,useState,type PointerEvent} from 'react';
import './PdfPinDock.css';

export interface DockItem {
  id:string;
  title:string;
  kind:'region'|'formula'|'table'|'image';
}

interface Props {
  items:DockItem[];
  onRestore:(id:string)=>void;
  storageKey?:string;
  language?:'zh'|'en';
}

type Position={key?:string;x:number;y:number};
type Drag={pointerId:number;startX:number;startY:number;x:number;y:number;moved:boolean};
const SIZE=54;
const WHEEL=206;
const EDGE=8;
const STORAGE_PREFIX='lazymind:pdf-pin-dock:';
const clamp=(value:number,min:number,max:number)=>Math.min(Math.max(value,min),Math.max(min,max));
const viewportPosition=(x:number,y:number)=>({
  x:clamp(x,EDGE,Math.max(EDGE,window.innerWidth-SIZE-EDGE)),
  y:clamp(y,EDGE,Math.max(EDGE,window.innerHeight-SIZE-EDGE)),
});

function readPosition(key?:string):Position{
  let saved:{x?:number;y?:number}|undefined;
  if(key){
    try {saved=JSON.parse(window.localStorage.getItem(STORAGE_PREFIX+key)||'null')||undefined;}
    catch { /* Restricted storage must not hide reading controls. */ }
  }
  // Start beside the reader's floating tools, clear of its bottom-right bubble stack.
  const x=Number.isFinite(saved?.x)?saved!.x!:window.innerWidth-210;
  const y=Number.isFinite(saved?.y)?saved!.y!:(window.innerHeight>500?window.innerHeight-300:window.innerHeight/2-SIZE/2);
  return {key,...viewportPosition(x,y)};
}

/** One movable, six-slot home for minimized PDF reading windows. */
export function PdfPinDock({items,onRestore,storageKey,language='zh'}:Props){
  const zh=language==='zh';
  const [position,setPosition]=useState<Position>(()=>readPosition(storageKey));
  const [expanded,setExpanded]=useState(false);
  const [dragging,setDragging]=useState(false);
  const [viewportSize,setViewportSize]=useState({width:window.innerWidth,height:window.innerHeight});
  const drag=useRef<Drag>();
  const suppressClick=useRef(false);
  const suppressTimer=useRef<number>();
  const hubRef=useRef<HTMLButtonElement>(null);

  useEffect(()=>{
    setPosition(current=>current.key===storageKey?current:readPosition(storageKey));
    setExpanded(false);
  },[storageKey]);
  useEffect(()=>{
    if(!storageKey||position.key!==storageKey)return;
    try {window.localStorage.setItem(STORAGE_PREFIX+storageKey,JSON.stringify({x:position.x,y:position.y}));}
    catch { /* Storage quotas or private mode must not break the dock. */ }
  },[position,storageKey]);
  useEffect(()=>{
    const update=()=>{
      setViewportSize({width:window.innerWidth,height:window.innerHeight});
      setPosition(current=>({...current,...viewportPosition(current.x,current.y)}));
    };
    window.addEventListener('resize',update);
    return()=>{window.removeEventListener('resize',update);window.clearTimeout(suppressTimer.current);};
  },[]);

  if(!items.length)return null;

  const visible=items.slice(0,6);
  const wheelInset=WHEEL/2-SIZE/2+EDGE;
  const displayed=expanded?{
    x:clamp(position.x,wheelInset,Math.max(wheelInset,viewportSize.width-SIZE-wheelInset)),
    y:clamp(position.y,wheelInset,Math.max(wheelInset,viewportSize.height-SIZE-wheelInset)),
  }:position;
  const onPointerDown=(event:PointerEvent<HTMLButtonElement>)=>{
    if(event.button!==0)return;
    drag.current={pointerId:event.pointerId,startX:event.clientX,startY:event.clientY,x:displayed.x,y:displayed.y,moved:false};
    event.currentTarget.setPointerCapture?.(event.pointerId);
  };
  const onPointerMove=(event:PointerEvent<HTMLButtonElement>)=>{
    const current=drag.current;
    if(!current||current.pointerId!==event.pointerId)return;
    const dx=event.clientX-current.startX,dy=event.clientY-current.startY;
    if(!current.moved&&Math.hypot(dx,dy)<6)return;
    if(!current.moved){current.moved=true;setDragging(true);setExpanded(false);}
    setPosition(previous=>({...previous,...viewportPosition(current.x+dx,current.y+dy)}));
    event.preventDefault();
  };
  const onPointerEnd=(event:PointerEvent<HTMLButtonElement>)=>{
    if(drag.current?.pointerId!==event.pointerId)return;
    if(drag.current.moved){
      suppressClick.current=true;
      window.clearTimeout(suppressTimer.current);
      suppressTimer.current=window.setTimeout(()=>{suppressClick.current=false;},0);
    }
    drag.current=undefined;
    setDragging(false);
  };

  return createPortal(<div className={`pdf-pin-dock${expanded?' is-expanded':''}${dragging?' is-dragging':''}`} style={{left:displayed.x,top:displayed.y}}
    onKeyDown={event=>{if(event.key==='Escape'&&expanded){event.stopPropagation();setExpanded(false);hubRef.current?.focus();}}}>
    {expanded&&<div className="pdf-pin-dock-wheel" role="group" aria-label={zh?'已收起的钉住内容':'Minimized pins'}>
      {Array.from({length:6},(_,index)=>{
        const item=visible[index];
        const angle=(-90+index*60)*Math.PI/180;
        const left=WHEEL/2+Math.cos(angle)*76;
        const top=WHEEL/2+Math.sin(angle)*76;
        return <button key={item?.id||`empty-${index}`} type="button" data-dock-slot={index}
          className={`pdf-pin-dock-slot${item?' has-item':''}`}
          style={{left,top}}
          data-label={item?.title}
          aria-label={item?(zh?`恢复${item.title}`:`Restore ${item.title}`):(zh?`空位置 ${index+1}`:`Empty slot ${index+1}`)}
          title={item?.title}
          disabled={!item}
          onClick={()=>{if(item){onRestore(item.id);setExpanded(false);}}}>
          {item?<><span aria-hidden="true" className={`pdf-pin-dock-glyph is-${item.kind}`}>{item.kind==='region'?'▣':item.kind==='formula'?'ƒ':item.kind==='table'?'▦':'◇'}</span><span className="pdf-pin-dock-index" aria-hidden="true">{index+1}</span></>:<span aria-hidden="true" className="pdf-pin-dock-empty"/>}
        </button>;
      })}
    </div>}
    <button ref={hubRef} type="button" className="pdf-pin-dock-hub"
      aria-label={expanded?(zh?'收起钉住内容':'Hide minimized pins'):(zh?`展开已收起内容（${items.length}）`:`Show minimized pins (${items.length})`)}
      aria-expanded={expanded}
      title={zh?`${items.length} 个已收起内容 · 拖动可移动`:`${items.length} minimized pins · drag to move`}
      onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={onPointerEnd} onPointerCancel={onPointerEnd}
      onClick={()=>{if(suppressClick.current){suppressClick.current=false;return;}setExpanded(value=>!value);}}>
      <span className="pdf-pin-dock-hub-core" aria-hidden="true">{Math.min(items.length,6)}</span>
    </button>
  </div>,window.document.body);
}

export default PdfPinDock;
