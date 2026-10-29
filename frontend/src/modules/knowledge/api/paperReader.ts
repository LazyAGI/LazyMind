import {useCallback,useEffect,useRef,useState} from 'react';
import {AgentAppsAuth,AUTH_USER_CHANGE_EVENT,AUTH_LOGOUT_EVENT} from '@/components/auth';
import {axiosInstance,BASE_URL} from '@/components/request';
import type {PdfIRDocument,PdfIRTarget} from './pdfIR';
import type {FormulaEvidence} from './formulaExplanation';
export const PAPER_STAGES=['problem','insight','method','evidence','limitations'] as const;
export interface PaperGuide {source_sha256:string;stages:Array<{key:typeof PAPER_STAGES[number];title:string;summary:string;evidence_ids:string[];related_block_ids:string[];inferred:boolean}>;evidence:FormulaEvidence[];model?:string;warnings:string[]}
export interface VisualExplanation {source_sha256:string;summary:string;observations:Array<{text:string;evidence_ids:string[];inferred:boolean}>;evidence:FormulaEvidence[];model?:string;warnings:string[]}
export function paperSourceKey(target:PdfIRTarget,document:PdfIRDocument){return `${target.dataset}/${target.document}/${document.source_sha256}/${document.version}/${document.projection_version||0}/${document.parser.name}/${document.parser.model}`;}
export function usePaperReaderSession(){
 const [identity,setIdentity]=useState(()=>AgentAppsAuth.getSessionIdentity());
 useEffect(()=>{const update=()=>setIdentity(AgentAppsAuth.getSessionIdentity());window.addEventListener(AUTH_USER_CHANGE_EVENT,update);window.addEventListener(AUTH_LOGOUT_EVENT,update);return()=>{window.removeEventListener(AUTH_USER_CHANGE_EVENT,update);window.removeEventListener(AUTH_LOGOUT_EVENT,update);};},[]);
 return identity;
}
export function usePaperReaderResource<T extends {source_sha256:string}>(target:PdfIRTarget,sourceKey:string,sourceHash:string,resource:string,language:string,active:boolean){
 const identity=usePaperReaderSession();
 const key=`${identity}/${target.dataset}/${target.document}/${sourceKey}/${resource}/${language}`;
 const [state,setState]=useState<{key:string;status:'loading'|'ready'|'error'|'cancelled';data?:T;error?:string;sourceMismatch?:boolean}>();
 const current=useRef<AbortController>();
 const load=useCallback(async(force=false)=>{
  current.current?.abort();
  const controller=new AbortController();current.current=controller;setState({key,status:'loading'});
  try{
   const url=`${BASE_URL}/api/core/datasets/${encodeURIComponent(target.dataset)}/documents/${encodeURIComponent(target.document)}/pdf-ir/${resource}`;
   const response=await axiosInstance.post<{data:T}>(url,{language,force,source_sha256:sourceHash},{signal:controller.signal,timeout:120000,silentError:true} as never);
   if(controller.signal.aborted)return;
   if(response.data.data.source_sha256!==sourceHash){setState({key,status:'error',sourceMismatch:true});return;}
   setState({key,status:'ready',data:response.data.data});
  }catch(error){if(!controller.signal.aborted){const detail=(error as {response?:{data?:{data?:{error_message?:string}}}})?.response?.data?.data?.error_message;setState({key,status:'error',error:detail});}}
 },[key,target.dataset,target.document,sourceHash,resource,language]);
 useEffect(()=>{if(active)void load();return()=>current.current?.abort();},[active,load]);
 const cancel=()=>{current.current?.abort();setState({key,status:'cancelled'});};
 return {...(state?.key===key?state:undefined),load,cancel};
}
// Convert textual PDF markup into readable text without injecting it into DOM.
export function readablePaperText(text:string):string {
 if(!/<\/?[a-z][^>]*>|&(?:[a-z]+|#\d+|#x[a-f0-9]+);/i.test(text))return text;
 const math:string[]=[];
 const protectedText=text.replace(/\\\([\s\S]*?\\\)|\\\[[\s\S]*?\\\]|\$\$[\s\S]*?\$\$|\$[^\n$]*?\$/g,value=>{math.push(value);return `\uE000${math.length-1}\uE001`;});
 const stripped=protectedText.replace(/<(script|style|iframe|object)\b[^>]*>[\s\S]*?<\/\1\s*>/gi,'').replace(/<br\s*\/?>/gi,'\n').replace(/<[^>]*>/g,'');
 const textarea=globalThis.document.createElement('textarea');
 textarea.innerHTML=stripped.replace(/</g,'&lt;').replace(/>/g,'&gt;');
 return textarea.value.replace(/\uE000(\d+)\uE001/g,(_,index)=>math[Number(index)] || '');
}
