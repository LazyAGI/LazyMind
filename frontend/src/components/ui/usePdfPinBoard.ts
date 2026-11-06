import {useCallback,useEffect,useRef,useState} from 'react';
import {readPdfPinBoard,writePdfPinBoard,type PdfPinRecord} from './pdfPinStore';

const MAX_PINS=6;

/** Keeps a paper's six pins responsive in memory while IndexedDB hydrates and saves. */
export function usePdfPinBoard(storageKey:string|undefined,source:unknown){
  const [state,setState]=useState<{key?:string;source:unknown;records:PdfPinRecord[];ready:boolean}>({key:storageKey,source,records:[],ready:!storageKey});
  const [saveError,setSaveError]=useState(false);
  const latest=useRef<PdfPinRecord[]>([]);
  const revision=useRef(0);
  const readyRef=useRef(!storageKey);
  const scopeRef=useRef<{key?:string;source:unknown}>({key:storageKey,source});
  const records=state.key===storageKey&&state.source===source?state.records:[];
  const ready=state.key===storageKey&&state.source===source&&state.ready;

  useEffect(()=>{
    let cancelled=false;
    latest.current=[];
    readyRef.current=!storageKey;
    scopeRef.current={key:storageKey,source};
    setSaveError(false);
    const generation=++revision.current;
    setState({key:storageKey,source,records:[],ready:!storageKey});
    if(!storageKey)return()=>{cancelled=true;};
    void readPdfPinBoard(storageKey).then(saved=>{
      if(cancelled||revision.current!==generation)return;
      latest.current=saved;
      readyRef.current=true;
      setState({key:storageKey,source,records:saved,ready:true});
    });
    return()=>{cancelled=true;};
  },[storageKey,source]);

  const change=useCallback((update:(records:PdfPinRecord[])=>PdfPinRecord[]|undefined)=>{
    if(!readyRef.current||scopeRef.current.key!==storageKey||scopeRef.current.source!==source)return false;
    const next=update(latest.current);
    if(!next||next===latest.current)return false;
    latest.current=next;
    revision.current++;
    setState({key:storageKey,source,records:next,ready:true});
    void writePdfPinBoard(storageKey,next).then(saved=>{
      if(scopeRef.current.key===storageKey&&scopeRef.current.source===source)setSaveError(!saved&&!!storageKey);
    });
    return true;
  },[storageKey,source]);

  const add=useCallback((record:PdfPinRecord)=>change(current=>{
    const existing=current.findIndex(item=>item.id===record.id);
    if(existing>=0)return current.map(item=>item.id===record.id?record:item);
    if(current.length>=MAX_PINS)return undefined;
    return [...current,record];
  }),[change]);
  const update=useCallback((id:string,patch:Partial<PdfPinRecord>)=>change(current=>{
    if(!current.some(item=>item.id===id))return undefined;
    return current.map(item=>item.id===id?{...item,...patch} as PdfPinRecord:item);
  }),[change]);
  const remove=useCallback((id:string)=>change(current=>current.some(item=>item.id===id)?current.filter(item=>item.id!==id):undefined),[change]);
  return {records,ready,saveError,add,update,remove,change};
}
