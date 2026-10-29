import {useEffect,useMemo,useRef,useState} from 'react';
import {axiosInstance,BASE_URL} from '@/components/request';
import type {PdfIRBlock,PdfIRDocument,PdfIRTarget} from '@/modules/knowledge/api/pdfIR';
import {paperSourceKey,readablePaperText,usePaperReaderSession} from '@/modules/knowledge/api/paperReader';
import {FormulaMathText} from './FormulaMathText';
import {usePaperReaderLabels,paperVisualLabel} from './paperReaderLabels';
interface Props{target:PdfIRTarget;document:PdfIRDocument;active:boolean;onNavigate:(id:string)=>void;onStudy:(block:PdfIRBlock)=>void}
interface Translation {status:'loading'|'ready'|'error'|'cancelled';text?:string;empty?:boolean;error?:string}
const PARAGRAPH_LIMIT=8000;
export function PdfBilingualReader({target,document,active,onNavigate,onStudy}:Props){
 const {label}=usePaperReaderLabels();
 const session=usePaperReaderSession();
 const [page,setPage]=useState(document.pages[0]?.index || 0);const [language,setLanguage]=useState('zh');
 const scope=`${session}/${paperSourceKey(target,document)}/${language}`;
 const [result,setResult]=useState<{scope:string;items:Record<string,Translation>}>({scope:'',items:{}});
 const results=result.scope===scope?result.items:{};
 const requests=useRef(new Map<string,AbortController>());const generation=useRef(0);
 const cache=useMemo(()=>new Map<string,string>(),[scope]);
 const pageBlocks=useMemo(()=>document.blocks.filter(block=>block.page===page),[document.blocks,page]);
 const paragraphs=pageBlocks.filter(block=>!['formula','table','image','page_number','header','footer'].includes(block.type)&&!!block.text?.trim());
 const update=(id:string,value:Translation)=>setResult(current=>({scope,items:{...(current.scope===scope?current.items:{}),[id]:value}}));
 const cancel=()=>{generation.current++;requests.current.forEach(controller=>controller.abort());requests.current.clear();setResult(current=>current.scope===scope?{scope,items:Object.fromEntries(Object.entries(current.items).map(([id,item])=>[id,item.status==='loading'?{status:'cancelled'}:item]))}:current);};
 useEffect(()=>{return()=>{generation.current++;requests.current.forEach(controller=>controller.abort());requests.current.clear();setResult(current=>current.scope===scope?{scope,items:Object.fromEntries(Object.entries(current.items).map(([id,item])=>[id,item.status==='loading'?{status:'cancelled'}:item]))}:current);};},[scope,page,active]);
 useEffect(()=>{setPage(document.pages[0]?.index || 0);},[document.source_sha256,target.document,target.dataset]);
 const translate=async(block:PdfIRBlock,batchGeneration=generation.current,force=false)=>{
  if(!active || requests.current.has(block.id) || batchGeneration!==generation.current)return;
  const text=readablePaperText(block.text || '');if(!text.trim()||text.length>PARAGRAPH_LIMIT)return;
  const cached=cache.get(block.id);if(cached&&!force){update(block.id,{status:'ready',text:cached});return;}
  const controller=new AbortController();requests.current.set(block.id,controller);update(block.id,{status:'loading'});
  try{
   const url=`${BASE_URL}/api/core/datasets/${encodeURIComponent(target.dataset)}/documents/${encodeURIComponent(target.document)}/pdf-ir/blocks/${encodeURIComponent(block.id)}/translation`;
   const response=await axiosInstance.post<{data:{translated_text:string;block_id:string;language:string;source_sha256:string}}>(url,{language,force,source_sha256:document.source_sha256},{signal:controller.signal,timeout:120000,silentError:true} as never);
   if(controller.signal.aborted||batchGeneration!==generation.current)return;
   if(response.data.data.source_sha256!==document.source_sha256||response.data.data.block_id!==block.id||response.data.data.language!==language){update(block.id,{status:'error'});return;}
   const translated=readablePaperText(response.data.data.translated_text || "");
   if(!translated?.trim()){update(block.id,{status:'error',empty:true});return;}
   cache.set(block.id,translated);update(block.id,{status:'ready',text:translated});
  }catch(error){if(!controller.signal.aborted&&batchGeneration===generation.current)update(block.id,{status:'error',error:(error as {response?:{data?:{data?:{error_message?:string}}}})?.response?.data?.data?.error_message});}
  finally{if(requests.current.get(block.id)===controller)requests.current.delete(block.id);}
 };
 const translatePage=async()=>{
  const batchGeneration=generation.current;
  const queue=paragraphs.filter(block=>!cache.has(block.id)&&readablePaperText(block.text || '').length<=PARAGRAPH_LIMIT).slice(0,20);
  let next=0;
  const worker=async()=>{while(next<queue.length&&batchGeneration===generation.current){const block=queue[next++];await translate(block,batchGeneration);}};
  await Promise.all([worker(),worker()]);
 };
 const busy=Object.values(results).some(item=>item.status==='loading');
 return <div className="pdf-bilingual-reader"><div className="pdf-bilingual-toolbar"><label>{label('page')}<select aria-label={label('page')} value={page} onChange={event=>{cancel();setPage(Number(event.target.value));}}>{document.pages.map(item=><option key={item.index} value={item.index}>{label('pageLabel',{page:item.index+1})}</option>)}</select></label><label>{label('translationTarget')}<select aria-label={label('translationTarget')} value={language} onChange={event=>{cancel();setLanguage(event.target.value);}}><option value="zh">{label('translateZh')}</option><option value="en">{label('translateEn')}</option></select></label><button disabled={busy||!paragraphs.length} onClick={()=>void translatePage()}>{label('translatePage')}</button>{busy&&<button onClick={cancel}>{label('cancel')}</button>}</div>
  <p className="pdf-bilingual-note">{label('translationHint')}</p>{paragraphs.length>20&&<p className="pdf-bilingual-note">{label('pageBound')}</p>}
  {!paragraphs.length&&<p>{label('noParagraphs')}</p>}
  {pageBlocks.map(block=>{
   if(['formula','table','image'].includes(block.type))return <div className="pdf-bilingual-visual" key={block.id}><span>{paperVisualLabel(block,document.blocks,label)}</span><p><FormulaMathText mathOnly={block.type==='formula'} text={readablePaperText(block.latex || block.table?.captions.join('\n') || block.image?.captions.join('\n') || block.text || '')}/></p><button onClick={()=>onStudy(block)}>{label('source')}</button></div>;
   if(!paragraphs.includes(block))return null;
   const text=readablePaperText(block.text || '');const state=results[block.id];const tooLong=text.length>PARAGRAPH_LIMIT;
   return <section key={block.id} className="pdf-bilingual-paragraph"><div className="pdf-bilingual-source"><p><FormulaMathText text={text}/></p><button className="pdf-reader-source-link" onClick={()=>onNavigate(block.id)}>{label('sourcePage',{page:block.page+1})}</button></div><div className={`pdf-bilingual-translation is-${state?.status || 'idle'}`}>
    {state?.status==='ready'?<><p><FormulaMathText text={state.text || ''}/></p><button disabled={busy} onClick={()=>void translate(block,generation.current,true)}>{label('regenerate')}</button></>:state?.status==='loading'?<p role="status">{label('translationLoading')}</p>:<><p role={state?.status==='error'?'alert':undefined}>{tooLong?label('translationTooLong'):state?.status==='error'?(state.error || label(state.empty?'translationEmpty':'translationError')):state?.status==='cancelled'?label('translationCancelled'):label('translationPending')}</p>{!tooLong&&<button disabled={busy} onClick={()=>void translate(block)}>{state?.status==='error'?label('retry'):label('translateParagraph')}</button>}</>}
   </div></section>;
  })}
 </div>;
}
