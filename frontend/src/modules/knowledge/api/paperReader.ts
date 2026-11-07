import {useCallback,useEffect,useRef,useState} from 'react';
import {AgentAppsAuth,AUTH_USER_CHANGE_EVENT,AUTH_LOGOUT_EVENT} from '@/components/auth';
import {axiosInstance,BASE_URL} from '@/components/request';
import type {PdfIRDocument,PdfIRTarget} from './pdfIR';
import type {FormulaEvidence} from './formulaExplanation';
export interface VisualExplanation {source_sha256:string;summary:string;observations:Array<{text:string;evidence_ids:string[];inferred:boolean}>;evidence:FormulaEvidence[];model?:string;warnings:string[]}
export function paperSourceKey(target:PdfIRTarget,document:PdfIRDocument){return `${target.dataset}/${target.document}/${document.source_sha256}/${document.version}/${document.projection_version||0}/${document.parser.name}/${document.parser.model}`;}
export function usePaperReaderSession(){
 const [identity,setIdentity]=useState(()=>AgentAppsAuth.getSessionIdentity());
 useEffect(()=>{const update=()=>setIdentity(AgentAppsAuth.getSessionIdentity());window.addEventListener(AUTH_USER_CHANGE_EVENT,update);window.addEventListener(AUTH_LOGOUT_EVENT,update);return()=>{window.removeEventListener(AUTH_USER_CHANGE_EVENT,update);window.removeEventListener(AUTH_LOGOUT_EVENT,update);};},[]);
 return identity;
}
// Source- and account-scoped, bounded cache: reopening a study is immediate.
const readingCache=new Map<string,{data:{source_sha256:string};expires:number}>();
if(typeof window!=='undefined')for(const event of [AUTH_USER_CHANGE_EVENT,AUTH_LOGOUT_EVENT])window.addEventListener(event,()=>readingCache.clear());
function readCachedReading<T>(key:string):T|undefined{
 const entry=readingCache.get(key);
 if(!entry)return;
 if(entry.expires<=Date.now()){readingCache.delete(key);return;}
 readingCache.delete(key);readingCache.set(key,entry);
 return entry.data as T;
}
function cacheReading(key:string,data:{source_sha256:string}){
 readingCache.delete(key);readingCache.set(key,{data,expires:Date.now()+300_000});
 if(readingCache.size>64)readingCache.delete(readingCache.keys().next().value!);
}
export function usePaperReaderResource<T extends {source_sha256:string}>(target:PdfIRTarget,sourceKey:string,sourceHash:string,resource:string,language:string,active:boolean,initialExplanation?:T,onExplanationChange?:(data:T)=>void){
 const identity=usePaperReaderSession();
 const key=`${identity}/${target.dataset}/${target.document}/${sourceKey}/${resource}/${language}`;
 const saved=initialExplanation?.source_sha256===sourceHash?initialExplanation:undefined;
 const [state,setState]=useState<{key:string;status:'idle'|'loading'|'ready'|'error'|'cancelled';data?:T;error?:string;sourceMismatch?:boolean}|undefined>(()=>saved?{key,status:'ready',data:saved}:undefined);
 const current=useRef<AbortController>();
 const onExplanationChangeRef=useRef(onExplanationChange);
 onExplanationChangeRef.current=onExplanationChange;
 const load=useCallback(async(force=false)=>{
  current.current?.abort();
  const cached=readCachedReading<T>(key);
  if(cached&&!force){setState({key,status:'ready',data:cached});onExplanationChangeRef.current?.(cached);return;}
  const controller=new AbortController();current.current=controller;setState(previous=>({key,status:'loading',data:previous?.key===key?previous.data:cached}));
  try{
   const url=`${BASE_URL}/api/core/datasets/${encodeURIComponent(target.dataset)}/documents/${encodeURIComponent(target.document)}/pdf-ir/${resource}`;
   const response=await axiosInstance.post<{data:T}>(url,{language,force,source_sha256:sourceHash},{signal:controller.signal,timeout:120000,silentError:true} as never);
   if(controller.signal.aborted||AgentAppsAuth.getSessionIdentity()!==identity)return;
   if(response.data.data.source_sha256!==sourceHash){readingCache.delete(key);setState({key,status:'error',sourceMismatch:true});return;}
   cacheReading(key,response.data.data);
   setState({key,status:'ready',data:response.data.data});
   onExplanationChangeRef.current?.(response.data.data);
  }catch(error){if(!controller.signal.aborted&&AgentAppsAuth.getSessionIdentity()===identity){const detail=(error as {response?:{data?:{data?:{error_message?:string}}}})?.response?.data?.data?.error_message;setState(previous=>({key,status:'error',data:previous?.key===key?previous.data:undefined,error:typeof detail==='string'?detail:undefined}));}}
 },[key,target.dataset,target.document,sourceHash,resource,language,identity]);
 useEffect(()=>{
  if(saved){
   current.current?.abort();
   cacheReading(key,saved);
   setState({key,status:'ready',data:saved});
  }else if(active)void load();
  else{
   const cached=readCachedReading<T>(key);
   setState(cached?{key,status:'ready',data:cached}:{key,status:'idle'});
   if(cached)onExplanationChangeRef.current?.(cached);
  }
  return()=>current.current?.abort();
 },[active,load,key,saved]);
 const cancel=()=>{current.current?.abort();setState(previous=>({key,status:'cancelled',data:previous?.key===key?previous.data:undefined}));};
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
