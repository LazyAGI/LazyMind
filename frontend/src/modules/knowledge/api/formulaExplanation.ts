import {useTranslation} from 'react-i18next';
import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {axiosInstance, BASE_URL} from '@/components/request';
import type {PdfIRTarget} from './pdfIR';
export interface FormulaEvidence {id:string; text:string; page:number; bbox?:[number,number,number,number]}
export interface FormulaExplanation {
  summary:string;
  symbols:Array<{symbol:string; meaning:string; evidence_ids:string[]; inferred:boolean}>;
  steps:Array<{text:string; evidence_ids:string[]; inferred:boolean}>;
  evidence:FormulaEvidence[]; warnings:string[]; model?:string;
}
export function useFormulaExplanation(target:PdfIRTarget, sourceKey:string, blockId?:string, autoExplain=false) {
  const {i18n}=useTranslation();
  const language=i18n.resolvedLanguage?.startsWith('zh') || i18n.language?.startsWith('zh') ? 'zh' : 'en';
  const scope=`${target.dataset}/${target.document}/${sourceKey}/${language}`;
  const cache=useMemo(()=>new Map<string,FormulaExplanation>(),[scope]);
  const controller=useRef<AbortController>();
  const key=`${scope}/${blockId || ''}`;
  const [state,setState]=useState<{key:string;status:'idle'|'loading'|'ready'|'error'|'cancelled';data?:FormulaExplanation;error?:string}>({key:'',status:'idle'});
  const load=useCallback(async(force=false)=>{
    controller.current?.abort();
    if(!blockId)return;
    const cached=cache.get(blockId);
    if(cached && !force){setState({key,status:'ready',data:cached});return;}
    const request=new AbortController();controller.current=request;
    setState({key,status:'loading'});
    try {
      const url=`${BASE_URL}/api/core/datasets/${encodeURIComponent(target.dataset)}/documents/${encodeURIComponent(target.document)}/pdf-ir/formulas/${encodeURIComponent(blockId)}/explanation`;
      const response=await axiosInstance.post<{data:FormulaExplanation}>(url,{force,language},{signal:request.signal,timeout:120000,silentError:true} as never);
      if(request.signal.aborted)return;
      cache.set(blockId,response.data.data);
      setState({key,status:'ready',data:response.data.data});
    } catch(error) {
      const detail=(error as {response?:{data?:{data?:{error_message?:string}}}})?.response?.data?.data?.error_message;
      if(!request.signal.aborted)setState({key,status:'error',error:typeof detail==='string'?detail:undefined});
    }
  },[target.dataset,target.document,blockId,cache,key,language]);
  useEffect(()=>{
    if(autoExplain)void load();
    else if(blockId && cache.has(blockId))setState({key,status:'ready',data:cache.get(blockId)});
    else setState({key,status:'idle'});
    return()=>controller.current?.abort();
  },[autoExplain,blockId,cache,key,load]);
  const cancel=()=>{controller.current?.abort();setState({key,status:'cancelled'});};
  return {...(state.key===key?state:{status:'idle' as const,data:undefined}),load,cancel};
}

export async function requestFocusedFormulaExplanation(target:PdfIRTarget,blockId:string,focusSymbol:string,language:'zh'|'en',signal:AbortSignal,force=false):Promise<FormulaExplanation> {
 const url=`${BASE_URL}/api/core/datasets/${encodeURIComponent(target.dataset)}/documents/${encodeURIComponent(target.document)}/pdf-ir/formulas/${encodeURIComponent(blockId)}/explanation`;
 const response=await axiosInstance.post<{data:FormulaExplanation}>(url,{force,language,focus_symbol:focusSymbol},{signal,timeout:120000,silentError:true} as never);
 return response.data.data;
}
