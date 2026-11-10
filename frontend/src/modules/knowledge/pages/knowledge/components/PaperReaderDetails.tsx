import {useEffect,useId,useRef,useState,type ReactNode} from 'react';
import {useTranslation} from 'react-i18next';
import {axiosInstance,BASE_URL} from '@/components/request';
import {addLibraryNote,decodePersistedLibrary,deleteLibraryNote,mergeLibraryChanges,type PersistedPaperLibrary} from '@/modules/knowledge/paper/store';
import type {NoteColor,PdfNoteSource} from '@/modules/knowledge/paper/libraryModel';
import {safePaperUrl,type PaperMetadata} from '@/modules/knowledge/paper/model';
import {PDF_NOTE_COLORS} from '@/components/ui/pdfNoteHighlights';
import './PaperReaderDetails.scss';
export interface PaperReaderDetailsProps {documentVocabulary?:ReactNode;datasetId:string;documentId:string;fileName:string;canWrite:boolean;draftRequest?:{documentId:string;requestId:number;text:string;sourceQuote?:string};initialTab?:'info'|'notes'|'vocabulary';onTabChange?:(tab:'notes'|'vocabulary')=>void;refreshToken?:number;selectedNoteId?:string;onNavigatePage?:(page:number)=>void;onNavigateNote?:(source:PdfNoteSource)=>void;onNoteDeleted?:(id:string)=>void}
function readableNote(content:string){
 const lines=content.replace(/\r\n?/g,'\n').split('\n');let quoteLines=0;
 while(lines[quoteLines]?.startsWith('> '))quoteLines++;
 const page=quoteLines?lines[quoteLines-1].match(/\((?:第\s*(\d+)\s*页|page\s*(\d+))\)\s*$/i):null;
 if(!page)return {body:content};
 return {body:lines.slice(quoteLines).join('\n').trim(),page:Number(page[1]||page[2])};
}
const noteColorEnglish:Record<NoteColor,string>={red:'Red',orange:'Orange',yellow:'Yellow',green:'Green',blue:'Blue',indigo:'Indigo',purple:'Purple',rainbow:'Rainbow'};
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
export default function PaperReaderDetails({datasetId,documentId,fileName,canWrite,draftRequest,initialTab='info',refreshToken,selectedNoteId,onNavigatePage,onNavigateNote,onNoteDeleted,documentVocabulary,onTabChange}:PaperReaderDetailsProps){
 const {i18n}=useTranslation();const zh=i18n.language.startsWith('zh');const label=(en:string,cn:string)=>zh?cn:en;
 const [notesTab,setNotesTab]=useState<'notes'|'vocabulary'>(initialTab==='vocabulary'?'vocabulary':'notes');
 const tab=initialTab==='info'?'info':documentVocabulary?notesTab:'notes';
 const tabId=useId();
 const selectTab=(next:'notes'|'vocabulary')=>{setNotesTab(next);onTabChange?.(next);};
 useEffect(()=>{if(initialTab!=='info')setNotesTab(initialTab);},[initialTab]);
 useEffect(()=>{if(selectedNoteId)setNotesTab('notes');},[selectedNoteId]);
 const [result,setResult]=useState<{key:string;library?:PersistedPaperLibrary;error?:string}>();
 const [revision,setRevision]=useState(0);const [draft,setDraft]=useState('');const [saving,setSaving]=useState(false);const [saveError,setSaveError]=useState('');const [copyStatus,setCopyStatus]=useState('');const [deletingId,setDeletingId]=useState('');const [deleteError,setDeleteError]=useState<{id:string;message:string}>();
 const draftDocument=useRef<string>();const noteSources=useRef<string[]>([]);const editorRef=useRef<HTMLDetailsElement>(null);
 const generation=useRef(0);const noteId=useRef('');const key=`${datasetId}/${documentId}`;const currentKey=useRef(key);currentKey.current=key;const mounted=useRef(false);
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;};},[]);
 useEffect(()=>{
  const controller=new AbortController();generation.current+=1;
  if(draftDocument.current!==key){draftDocument.current=key;setDraft('');noteId.current='';noteSources.current=[];if(editorRef.current)editorRef.current.open=false;}
  setSaveError('');setSaving(false);setCopyStatus('');
   axiosInstance.get(`${BASE_URL}/api/core/academic/library`,{params:{target_dataset_id:datasetId,include_processing:false},signal:controller.signal,timeout:30000,silentError:true} as never)
   .then(response=>{if(!controller.signal.aborted&&currentKey.current===key)setResult({key,library:decodePersistedLibrary(response.data)});})
   .catch(error=>{if(!controller.signal.aborted&&currentKey.current===key)setResult({key,error:error instanceof Error?error.message:label('Could not load paper details.','无法加载论文资料。')});});
  return()=>controller.abort();
 },[datasetId,documentId,revision,refreshToken]);
 const appliedDraft=useRef('');
 useEffect(()=>{
  if(!draftRequest||draftRequest.documentId!==documentId||saving)return;
  const requestKey=`${key}:${draftRequest.requestId}`;
  if(appliedDraft.current===requestKey)return;
  appliedDraft.current=requestKey;setNotesTab('notes');
  if(editorRef.current)editorRef.current.open=true;
  if(draftRequest.sourceQuote)noteSources.current.push(draftRequest.sourceQuote);
  if(draftRequest.text.trim())setDraft(current=>current.trim()?`${current}\n\n${draftRequest.text}`:draftRequest.text);
  noteId.current='';
 },[draftRequest,key,documentId,saving]);
 const library=result?.key===key?result.library:undefined;
 const currentAttachmentIds=new Set(library?.data.attachments.filter(item=>item.contentRef===documentId).map(item=>item.id));
 const paperIds=new Set(library?.data.attachments.filter(item=>item.contentRef===documentId).map(item=>item.paperId));
 const paper=paperIds.size===1?library?.data.papers.find(item=>paperIds.has(item.id)):undefined;
 const metadata=paper?.metadata;const notes=paper?library!.data.notes.filter(note=>note.paperId===paper.id&&paper.noteIds.includes(note.id)).sort((left,right)=>String(right.createdAt||'').localeCompare(String(left.createdAt||''))):[];
 const annotations=paper?library!.data.annotations?.filter(item=>item.paperId===paper.id&&(!item.attachmentId||library!.data.attachments.some(a=>a.id===item.attachmentId&&a.contentRef===documentId)))||[]:[];
 const writable=canWrite&&library?.canWrite&&!!paper;
 const selectedNoteRef=useRef<HTMLLIElement>(null);
 useEffect(()=>{if(tab!=='notes'||!selectedNoteId||!selectedNoteRef.current)return;selectedNoteRef.current.focus({preventScroll:true});selectedNoteRef.current.scrollIntoView?.({block:'nearest'});},[selectedNoteId,notes.length,tab]);
 const copy=async(text:string)=>{try{await navigator.clipboard.writeText(text);if(mounted.current&&currentKey.current===key)setCopyStatus(label('Copied','已复制'));}catch{if(mounted.current&&currentKey.current===key)setCopyStatus(label('Could not copy. Please try again.','复制失败，请重试。'));}};
 const save=async()=>{
  if(!writable||!draft.trim()||saving||!paper||!library)return;
  const saveGeneration=generation.current;setSaving(true);setSaveError('');if(!noteId.current)noteId.current=crypto.randomUUID();
  try{const source=noteSources.current.join('\n');const content=source?`${source}\n\n${draft.trim()}`:draft.trim();const change=await addLibraryNote(datasetId,paper.id,noteId.current,content);if(!mounted.current||currentKey.current!==key||generation.current!==saveGeneration)return;
   setResult({key,library:{...library,data:mergeLibraryChanges(library.data,change)}});setDraft('');noteId.current='';noteSources.current=[];
  }catch(error){if(mounted.current&&currentKey.current===key&&generation.current===saveGeneration)setSaveError(error instanceof Error?error.message:label('Could not save note.','无法保存笔记。'));}
  finally{if(mounted.current&&currentKey.current===key&&generation.current===saveGeneration)setSaving(false);}
 };
 const removeNote=async(id:string)=>{
  if(!writable||!paper||!library||deletingId)return;
  if(!window.confirm(label('Delete this note? This cannot be undone.','删除这条笔记？此操作无法撤销。')))return;
  setDeletingId(id);setDeleteError(undefined);
  try{const change=await deleteLibraryNote(datasetId,paper.id,id);setResult({key,library:{...library,data:mergeLibraryChanges(library.data,change)}});onNoteDeleted?.(id);}
  catch(error){setDeleteError({id,message:error instanceof Error?error.message:label('Could not delete note.','无法删除笔记。')});}
  finally{setDeletingId('');}
 };
 return <aside className="paper-reader-details" aria-label={label('Paper details','论文资料')}>
  <header><h2>{tab==='info'?label('Paper information','论文信息'):documentVocabulary?label('Notes & vocabulary','笔记和生词'):label('Notes','笔记')}</h2></header>
   {tab!=='info'&&documentVocabulary&&<div role="tablist" aria-label={label('Notes & vocabulary','笔记和生词')}>
    {(['notes','vocabulary'] as const).map(value=><button key={value} type="button" role="tab" id={`${tabId}-${value}-tab`} aria-controls={`${tabId}-${value}-panel`} aria-selected={tab===value} tabIndex={tab===value?0:-1} onClick={()=>selectTab(value)} onKeyDown={event=>{
     if(!['ArrowLeft','ArrowRight','Home','End'].includes(event.key))return;
     event.preventDefault();const next=event.key==='Home'?'notes':event.key==='End'?'vocabulary':value==='notes'?'vocabulary':'notes';selectTab(next);document.getElementById(`${tabId}-${next}-tab`)?.focus();
    }}>{value==='notes'?label('Notes','笔记'):label('Vocabulary','生词')}</button>)}
   </div>}
  <div className="paper-reader-details__content">
   <div hidden={tab==='vocabulary'} role={tab!=='info'&&documentVocabulary?'tabpanel':undefined} id={`${tabId}-notes-panel`} aria-labelledby={tab!=='info'&&documentVocabulary?`${tabId}-notes-tab`:undefined}>
   {result?.key===key&&result.error?<div role="alert"><p>{result.error}</p><button onClick={()=>{setResult(undefined);setRevision(value=>value+1);}}>{label('Retry','重试')}</button></div>:!library?<p role="status">{label('Loading paper details…','正在加载论文资料…')}</p>:tab==='info'?<>
    <p className="paper-reader-details__eyebrow">{label('DOCUMENT INFORMATION','文档信息')}</p><h3>{metadata?.title||fileName}</h3>
    {metadata&&<><dl>{!!metadata.authors.length&&<><dt>{label('Authors','作者')}</dt><dd>{metadata.authors.join(', ')}</dd></>}{metadata.year>0&&<><dt>{label('Year','年份')}</dt><dd>{metadata.year}</dd></>}{metadata.venue&&<><dt>{label('Publication','刊物')}</dt><dd>{metadata.venue}</dd></>}{Object.entries(metadata.identifiers).filter(([,value])=>!!value).map(([name,value])=>{const url=safePaperUrl(name==='doi'?`https://doi.org/${value}`:name==='arxiv'?`https://arxiv.org/abs/${value}`:undefined);return <div key={name}><dt>{name.toUpperCase()}</dt><dd>{url?<a href={url} target="_blank" rel="noopener noreferrer">{value}</a>:value}</dd></div>;})}</dl>
     {metadata.abstract&&<section><h4>{label('Abstract','摘要')}</h4><p>{metadata.abstract}</p></section>}
     <div className="paper-reader-details__copy"><button onClick={()=>void copy([metadata.authors.join(', '),metadata.year>0?`(${metadata.year})`:'',metadata.title,metadata.venue,metadata.identifiers.doi].filter(Boolean).join('. '))}>{label('Copy citation','复制引用')}</button><button onClick={()=>void copy(bibliography(metadata))}>{label('Copy BibTeX','复制 BibTeX')}</button></div><span role="status">{copyStatus}</span>
    </>}
    {!paper&&<p className="paper-reader-details__muted">{paperIds.size>1?label('Multiple paper records are linked to this document. Open the paper library to resolve the association.','此文档关联了多个论文条目，请在论文库中处理关联。'):label('This document is not linked to a paper in this library.','此文档尚未关联到论文库中的论文条目。')}</p>}
   </>:<>
    {!paper?<p>{label('This document is not linked to a paper in this library.','此文档尚未关联到论文库中的论文条目。')}</p>:<>
     {!notes.length&&<p className="paper-reader-details__empty">{label('No notes yet. Select text to add one.','暂无笔记，划选正文即可添加。')}</p>}
     {!!notes.length&&<ol className="paper-reader-details__notes" aria-label={label('Saved notes','已保存的笔记')}>{notes.map(note=>{
       const readable=readableNote(note.content);const displayed=readable.body||label('Highlighted passage','仅高亮');
       const damaged = /[?\uFFFD]{8,}/.test(displayed) && (displayed.match(/[?\uFFFD]/g)?.length || 0) > displayed.length * .3;
       const repeatedTitle = displayed.replace(/\s+/g,' ').trim().startsWith(note.title.replace(/\s+/g,' ').trim());
       const sameAttachment=!!note.source&&currentAttachmentIds.has(note.source.attachmentId);
       const foreignAttachment=!!note.source&&!sameAttachment;
       const sourcePage=note.source&&Number.isInteger(note.source.page)&&note.source.page>0?note.source.page:undefined;
       const page=sourcePage||readable.page;
       const navigate=sameAttachment&&sourcePage&&onNavigateNote?()=>onNavigateNote(note.source!):!note.source&&readable.page&&onNavigatePage?()=>onNavigatePage(readable.page!):undefined;
       const color=PDF_NOTE_COLORS.find(item=>item.key===note.source?.color);
       const content=damaged?<>
         <h4>{label('Imported note text is damaged','导入笔记的文字已损坏')}</h4>
         <p className="paper-reader-details__muted">{label('The saved text contains replacement characters. Check the original note or a backup before importing it again.','保存的内容包含大量替代字符。请核对来源笔记或备份，恢复原文后重新导入。')}</p>
         <details><summary>{label('View original text','查看原始内容')}</summary><p>{displayed}</p></details>
       </>:<>
         {note.title&&!repeatedTitle&&!/^(?:新笔记|New note)$/.test(note.title)&&<strong className="paper-reader-details__note-title">{note.title}</strong>}
         <span className="paper-reader-details__note-body">{displayed}</span>
         {(page||foreignAttachment||color)&&<span className="paper-reader-details__note-meta">
           {color&&<span className="paper-reader-details__note-color" style={{background:color.swatch}} aria-label={label(noteColorEnglish[color.key],color.label)}/>}
           {page&&<span>{label(`Page ${page}`,`第 ${page} 页`)}</span>}
           {foreignAttachment&&<span>{label('Another attachment','其他附件')}</span>}
         </span>}
       </>;
       const selected=note.id===selectedNoteId;
       return <li ref={selected?selectedNoteRef:undefined} tabIndex={selected?-1:undefined} aria-current={selected?'true':undefined} className={`paper-reader-details__note${selected?' is-selected':''}`} key={note.id}><div className="paper-reader-details__note-row">{navigate&&!damaged?<button type="button" className="paper-reader-details__note-source" onClick={navigate}>{content}</button>:<div className="paper-reader-details__note-static">{content}</div>}{writable&&<button type="button" className="paper-reader-details__note-delete" aria-label={label('Delete note','删除笔记')} disabled={!!deletingId} onClick={()=>void removeNote(note.id)}>{deletingId===note.id?label('Deleting…','正在删除…'):label('Delete','删除')}</button>}</div>{deleteError?.id===note.id&&<p role="alert">{deleteError.message}</p>}</li>;
     })}</ol>}
     {!!annotations.length&&<section className="paper-reader-details__annotations"><h4>{label('Saved annotations','已保存的批注')}</h4><ol className="paper-reader-details__notes" aria-label={label('Saved annotations','已保存的批注')}>{annotations.map(item=>{
       const color=PDF_NOTE_COLORS.find(candidate=>candidate.key===item.color);
       const content=<><span className="paper-reader-details__note-body">{item.comment||label('Highlighted passage','仅高亮')}</span>{(item.page!==undefined||color)&&<span className="paper-reader-details__note-meta">{color&&<span className="paper-reader-details__note-color" style={{background:color.swatch}} aria-label={label(noteColorEnglish[color.key],color.label)}/>} {item.page!==undefined&&<span>{label(`Page ${item.page}`,`第 ${item.page} 页`)}</span>}</span>}</>;
       return <li className="paper-reader-details__note" key={item.id}>{item.page!==undefined&&onNavigatePage?<button type="button" className="paper-reader-details__note-source" onClick={()=>onNavigatePage(item.page!)}>{content}</button>:<div className="paper-reader-details__note-static">{content}</div>}</li>;
     })}</ol></section>}
     {writable?<details className="paper-reader-details__new-note" ref={editorRef}><summary>{label('Write a note','写笔记')}</summary><form onSubmit={event=>{event.preventDefault();void save();}}><label htmlFor="paper-reader-new-note">{label('New note','新笔记')}</label><textarea id="paper-reader-new-note" value={draft} disabled={saving} onChange={event=>{setDraft(event.target.value);noteId.current='';}} rows={5} placeholder={label('What would you like to remember?','想记住什么？')}/>{saveError&&<p role="alert">{saveError}</p>}<button type="submit" disabled={saving||!draft.trim()}>{saving?label('Saving…','正在保存…'):label('Save note','保存笔记')}</button></form></details>:<p className="paper-reader-details__muted">{label('You have read-only access to these notes.','你可以查看这些笔记，当前没有编辑权限。')}</p>}
    </>}
   </>}
   </div>
   {documentVocabulary&&<div hidden={tab!=='vocabulary'} role="tabpanel" id={`${tabId}-vocabulary-panel`} aria-labelledby={`${tabId}-vocabulary-tab`}>{documentVocabulary}</div>}
  </div>
 </aside>;
}
