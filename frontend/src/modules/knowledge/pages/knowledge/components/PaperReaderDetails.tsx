import {useEffect,useRef,useState} from 'react';
import {useTranslation} from 'react-i18next';
import {axiosInstance,BASE_URL} from '@/components/request';
import {addLibraryNote,decodePersistedLibrary,mergeLibraryChanges,type PersistedPaperLibrary} from '@/modules/knowledge/paper/store';
import {safePaperUrl,type PaperMetadata} from '@/modules/knowledge/paper/model';
import './PaperReaderDetails.scss';
export interface PaperReaderDetailsProps {datasetId:string;documentId:string;fileName:string;canWrite:boolean;draftRequest?:{documentId:string;requestId:number;text:string};initialTab?:'info'|'notes';onNavigatePage?:(page:number)=>void}
function bibEscape(value:string){return value.replace(/[\\{}&%$#_~^]/g,char=>char==='\\'?'\\textbackslash{}':char==='~'?'\\textasciitilde{}':char==='^'?'\\textasciicircum{}':`\\${char}`).replace(/[\r\n]+/g,' ');}
function bibliography(metadata:PaperMetadata){
 const fields:Record<string,string>={title:metadata.title};
 if(metadata.authors.length)fields.author=metadata.authors.join(' and ');
 if(metadata.year>0)fields.year=String(metadata.year);
 if(metadata.venue)fields.journal=metadata.venue;
 if(metadata.identifiers.doi)fields.doi=metadata.identifiers.doi;
 if(metadata.identifiers.arxiv)fields.eprint=metadata.identifiers.arxiv;
 return `@article{paper,\n${Object.entries(fields).map(([key,value])=>`  ${key} = {${bibEscape(value)}}`).join(',\n')}\n}`;
}
export default function PaperReaderDetails({datasetId,documentId,fileName,canWrite,draftRequest,initialTab='info',onNavigatePage}:PaperReaderDetailsProps){
 const {i18n}=useTranslation();const zh=i18n.language.startsWith('zh');const label=(en:string,cn:string)=>zh?cn:en;
 const tab=initialTab;const [result,setResult]=useState<{key:string;library?:PersistedPaperLibrary;error?:string}>();
 const [revision,setRevision]=useState(0);const [draft,setDraft]=useState('');const [saving,setSaving]=useState(false);const [saveError,setSaveError]=useState('');const [copyStatus,setCopyStatus]=useState('');
 const draftDocument=useRef<string>();
 const generation=useRef(0);const noteId=useRef('');const key=`${datasetId}/${documentId}`;const currentKey=useRef(key);currentKey.current=key;const mounted=useRef(false);
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;};},[]);
 useEffect(()=>{
  const controller=new AbortController();generation.current+=1;
  if(draftDocument.current!==key){draftDocument.current=key;setDraft('');noteId.current='';}
  setSaveError('');setSaving(false);setCopyStatus('');
  axiosInstance.get(`${BASE_URL}/api/core/academic/library`,{params:{target_dataset_id:datasetId},signal:controller.signal,timeout:30000,silentError:true} as never)
   .then(response=>{if(!controller.signal.aborted&&currentKey.current===key)setResult({key,library:decodePersistedLibrary(response.data)});})
   .catch(error=>{if(!controller.signal.aborted&&currentKey.current===key)setResult({key,error:error instanceof Error?error.message:label('Could not load paper details.','无法加载论文资料。')});});
  return()=>controller.abort();
 },[datasetId,documentId,revision]);
 const appliedDraft=useRef('');
 useEffect(()=>{
  if(!draftRequest||draftRequest.documentId!==documentId||saving)return;
  const requestKey=`${key}:${draftRequest.requestId}`;
  if(appliedDraft.current===requestKey)return;
  appliedDraft.current=requestKey;
  setDraft(current=>current.trim()?`${current}\n\n${draftRequest.text}`:draftRequest.text);
  noteId.current='';
 },[draftRequest,key,documentId,saving]);
 const library=result?.key===key?result.library:undefined;
 const paperIds=new Set(library?.data.attachments.filter(item=>item.contentRef===documentId).map(item=>item.paperId));
 const paper=paperIds.size===1?library?.data.papers.find(item=>paperIds.has(item.id)):undefined;
 const metadata=paper?.metadata;const notes=paper?library!.data.notes.filter(note=>note.paperId===paper.id&&paper.noteIds.includes(note.id)):[];
 const annotations=paper?library!.data.annotations?.filter(item=>item.paperId===paper.id&&(!item.attachmentId||library!.data.attachments.some(a=>a.id===item.attachmentId&&a.contentRef===documentId)))||[]:[];
 const writable=canWrite&&library?.canWrite&&!!paper;
 const copy=async(text:string)=>{try{await navigator.clipboard.writeText(text);if(mounted.current&&currentKey.current===key)setCopyStatus(label('Copied','已复制'));}catch{if(mounted.current&&currentKey.current===key)setCopyStatus(label('Could not copy. Please try again.','复制失败，请重试。'));}};
 const save=async()=>{
  if(!writable||!draft.trim()||saving||!paper||!library)return;
  const saveGeneration=generation.current;setSaving(true);setSaveError('');if(!noteId.current)noteId.current=crypto.randomUUID();
  try{const change=await addLibraryNote(datasetId,paper.id,noteId.current,draft.trim());if(!mounted.current||currentKey.current!==key||generation.current!==saveGeneration)return;
   setResult({key,library:{...library,data:mergeLibraryChanges(library.data,change)}});setDraft('');noteId.current='';
  }catch(error){if(mounted.current&&currentKey.current===key&&generation.current===saveGeneration)setSaveError(error instanceof Error?error.message:label('Could not save note.','无法保存笔记。'));}
  finally{if(mounted.current&&currentKey.current===key&&generation.current===saveGeneration)setSaving(false);}
 };
 return <aside className="paper-reader-details" aria-label={label('Paper details','论文资料')}>
  <header><h2>{tab==='info'?label('Paper information','论文信息'):label('Notes','笔记')}</h2></header>
  <div className="paper-reader-details__content">
   {result?.key===key&&result.error?<div role="alert"><p>{result.error}</p><button onClick={()=>{setResult(undefined);setRevision(value=>value+1);}}>{label('Retry','重试')}</button></div>:!library?<p role="status">{label('Loading paper details…','正在加载论文资料…')}</p>:tab==='info'?<>
    <p className="paper-reader-details__eyebrow">{label('DOCUMENT INFORMATION','文档信息')}</p><h3>{metadata?.title||fileName}</h3>
    {metadata&&<><dl>{!!metadata.authors.length&&<><dt>{label('Authors','作者')}</dt><dd>{metadata.authors.join(', ')}</dd></>}{metadata.year>0&&<><dt>{label('Year','年份')}</dt><dd>{metadata.year}</dd></>}{metadata.venue&&<><dt>{label('Publication','刊物')}</dt><dd>{metadata.venue}</dd></>}{Object.entries(metadata.identifiers).filter(([,value])=>!!value).map(([name,value])=>{const url=safePaperUrl(name==='doi'?`https://doi.org/${value}`:name==='arxiv'?`https://arxiv.org/abs/${value}`:undefined);return <div key={name}><dt>{name.toUpperCase()}</dt><dd>{url?<a href={url} target="_blank" rel="noopener noreferrer">{value}</a>:value}</dd></div>;})}</dl>
     {metadata.abstract&&<section><h4>{label('Abstract','摘要')}</h4><p>{metadata.abstract}</p></section>}
     <div className="paper-reader-details__copy"><button onClick={()=>void copy([metadata.authors.join(', '),metadata.year>0?`(${metadata.year})`:'',metadata.title,metadata.venue,metadata.identifiers.doi].filter(Boolean).join('. '))}>{label('Copy citation','复制引用')}</button><button onClick={()=>void copy(bibliography(metadata))}>{label('Copy BibTeX','复制 BibTeX')}</button></div><span role="status">{copyStatus}</span>
    </>}
    {!paper&&<p className="paper-reader-details__muted">{paperIds.size>1?label('Multiple paper records are linked to this document. Open the paper library to resolve the association.','此文档关联了多个论文条目，请在论文库中处理关联。'):label('This document is not linked to a paper in this library.','此文档尚未关联到论文库中的论文条目。')}</p>}
   </>:<>
    <p className="paper-reader-details__muted">{label('Notes belong to the paper and are shared across its attachments.','笔记保存于论文条目，并在其附件间共享。')}</p>
    {!paper?<p>{label('This document is not linked to a paper in this library.','此文档尚未关联到论文库中的论文条目。')}</p>:<>
     {!notes.length&&<p>{label('No notes yet. Capture an idea as you read.','还没有笔记，记下阅读时的想法。')}</p>}
     {notes.map(note=><article className="paper-reader-details__note" key={note.id}>{note.title&&<h4>{note.title}</h4>}<p>{note.content}</p></article>)}
     {!!annotations.length&&<section><h4>{label('Saved annotations','已保存的批注')}</h4>{annotations.map(item=><article className="paper-reader-details__note" key={item.id}>{item.quote&&<blockquote>{item.quote}</blockquote>}<p>{item.comment}</p>{item.page!==undefined&&onNavigatePage&&<button onClick={()=>onNavigatePage(item.page!)}>{label(`Page ${item.page}`,`第 ${item.page} 页`)}</button>}</article>)}</section>}
     {writable?<form onSubmit={event=>{event.preventDefault();void save();}}><label htmlFor="paper-reader-new-note">{label('New note','新笔记')}</label><textarea id="paper-reader-new-note" value={draft} disabled={saving} onChange={event=>{setDraft(event.target.value);noteId.current='';}} rows={5} placeholder={label('What would you like to remember?','想记住什么？')}/>{saveError&&<p role="alert">{saveError}</p>}<button type="submit" disabled={saving||!draft.trim()}>{saving?label('Saving…','正在保存…'):label('Save note','保存笔记')}</button></form>:<p className="paper-reader-details__muted">{label('You have read-only access to these notes.','你可以查看这些笔记，当前没有编辑权限。')}</p>}
    </>}
   </>}
  </div>
 </aside>;
}
