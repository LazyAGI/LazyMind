import {useTranslation} from 'react-i18next';
import {useCallback, useEffect, useRef, useState} from 'react';
import {axiosInstance, BASE_URL} from '@/components/request';
import {AgentAppsAuth, AUTH_LOGOUT_EVENT, AUTH_USER_CHANGE_EVENT} from '@/components/auth';
import type {PdfIRTarget} from './pdfIR';
import {usePaperReaderSession} from './paperReader';
export interface FormulaEvidence {id:string; text:string; page:number; bbox?:[number,number,number,number]}
export interface FormulaExplanation {
  summary:string;
  reading?:string;
  display_latex?:string;
  repair_note?:string;
  symbols:Array<{symbol:string; meaning:string; evidence_ids:string[]; inferred:boolean}>;
  steps:Array<{text:string; evidence_ids:string[]; inferred:boolean}>;
  evidence:FormulaEvidence[]; warnings:string[]; model?:string;
}
const formulaMemoryCache=new Map<string,{data:FormulaExplanation;expires:number}>();
const formulaMemoryCacheTTL=300_000;
const formulaMemoryCacheLimit=64;
if(typeof window!=='undefined'){
  for(const event of [AUTH_LOGOUT_EVENT,AUTH_USER_CHANGE_EVENT]){
    window.addEventListener(event,()=>formulaMemoryCache.clear());
  }
}
function readFormulaMemoryCache(key:string){
  const entry=formulaMemoryCache.get(key);
  if(!entry)return;
  if(entry.expires<=Date.now()){formulaMemoryCache.delete(key);return;}
  formulaMemoryCache.delete(key);
  formulaMemoryCache.set(key,entry);
  return entry.data;
}
function writeFormulaMemoryCache(key:string,data:FormulaExplanation){
  formulaMemoryCache.delete(key);
  formulaMemoryCache.set(key,{data,expires:Date.now()+formulaMemoryCacheTTL});
  if(formulaMemoryCache.size>formulaMemoryCacheLimit)formulaMemoryCache.delete(formulaMemoryCache.keys().next().value!);
}
export function useFormulaExplanation(target:PdfIRTarget, sourceKey:string, blockId?:string, autoExplain=false, initialExplanation?:FormulaExplanation, onExplanationChange?:(data:FormulaExplanation)=>void) {
  const {i18n}=useTranslation();
  const language=i18n.resolvedLanguage?.startsWith('zh') || i18n.language?.startsWith('zh') ? 'zh' : 'en';
  const identity=usePaperReaderSession();
  const scope=`${identity}/${target.dataset}/${target.document}/${sourceKey}/${language}`;
  const controller=useRef<AbortController>();
  const key=`${scope}/${blockId || ''}`;
  const onExplanationChangeRef=useRef(onExplanationChange);
  onExplanationChangeRef.current=onExplanationChange;
  const [state,setState]=useState<{key:string;status:'idle'|'loading'|'ready'|'error'|'cancelled';data?:FormulaExplanation;error?:string}>(()=>initialExplanation?{key,status:'ready',data:initialExplanation}:{key:'',status:'idle'});
  const load=useCallback(async(force=false)=>{
    controller.current?.abort();
    if(!blockId)return;
    const cached=readFormulaMemoryCache(key);
    if(cached && !force){setState({key,status:'ready',data:cached});onExplanationChangeRef.current?.(cached);return;}
    const request=new AbortController();controller.current=request;
    setState(previous=>({key,status:'loading',data:previous.key===key?previous.data:cached}));
    try {
      const url=`${BASE_URL}/api/core/datasets/${encodeURIComponent(target.dataset)}/documents/${encodeURIComponent(target.document)}/pdf-ir/formulas/${encodeURIComponent(blockId)}/explanation`;
      const response=await axiosInstance.post<{data:FormulaExplanation}>(url,{force,language},{signal:request.signal,timeout:120000,silentError:true} as never);
      if(request.signal.aborted || AgentAppsAuth.getSessionIdentity()!==identity)return;
      writeFormulaMemoryCache(key,response.data.data);
      setState({key,status:'ready',data:response.data.data});
      onExplanationChangeRef.current?.(response.data.data);
    } catch(error) {
      const detail=(error as {response?:{data?:{data?:{error_message?:string}}}})?.response?.data?.data?.error_message;
      if(!request.signal.aborted && AgentAppsAuth.getSessionIdentity()===identity)setState(previous=>({key,status:'error',data:previous.key===key?previous.data:undefined,error:typeof detail==='string'?detail:undefined}));
    }
  },[target.dataset,target.document,blockId,key,language,identity]);
  useEffect(()=>{
    if(initialExplanation){
      controller.current?.abort();
      writeFormulaMemoryCache(key,initialExplanation);
      setState({key,status:'ready',data:initialExplanation});
    }
    else if(autoExplain)void load();
    else {
      const cached=readFormulaMemoryCache(key);
      setState(cached?{key,status:'ready',data:cached}:{key,status:'idle'});
      if(cached)onExplanationChangeRef.current?.(cached);
    }
    return()=>controller.current?.abort();
  },[autoExplain,key,load,initialExplanation]);
  const cancel=()=>{controller.current?.abort();setState(previous=>({key,status:'cancelled',data:previous.key===key?previous.data:undefined}));};
  return {...(state.key===key?state:{status:'idle' as const,data:undefined}),load,cancel};
}
