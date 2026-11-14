import {axiosInstance,BASE_URL} from '@/components/request';
import type {PdfNoteHighlight} from '@/components/ui/pdfNoteHighlights';
import type {PaperLibraryData} from './model';
import type {PdfNoteSource} from './libraryModel';
import {addLibraryNote,decodePersistedLibrary,type PersistedPaperLibrary} from './store';

async function loadLibrary(datasetId:string):Promise<PersistedPaperLibrary>{
 const response=await axiosInstance.get(`${BASE_URL}/api/core/academic/library`,{params:{target_dataset_id:datasetId,include_processing:false},timeout:30000,silentError:true} as never);
 return decodePersistedLibrary(response.data);
}

function linkedDocument(data:PaperLibraryData,documentId:string){
 const attachments=data.attachments.filter(item=>item.contentRef===documentId);
 const paperIds=new Set(attachments.map(item=>item.paperId));
 if(paperIds.size!==1)throw new Error('此文档尚未唯一关联论文条目，无法保存笔记。');
 const paper=data.papers.find(item=>paperIds.has(item.id));
 const attachment=attachments.find(item=>item.paperId===paper?.id&&item.isPrimary)
  ||attachments.find(item=>item.paperId===paper?.id);
 if(!paper||!attachment)throw new Error('未找到当前 PDF 的论文附件，无法保存笔记。');
 return {paper,attachment};
}

/** Keep the PDF anchor on the server with the authored text, never inside Markdown. */
export async function savePdfSelectionNote(datasetId:string,documentId:string,content:string,source:Omit<PdfNoteSource,'attachmentId'>,id:string):Promise<void>{
 const library=await loadLibrary(datasetId);
 if(!library.canWrite)throw new Error('当前没有保存论文笔记的权限。');
 const {paper,attachment}=linkedDocument(library.data,documentId);
 const anchored:PdfNoteSource={...source,attachmentId:attachment.id,quote:source.quote?.slice(0,16000)};
 try{
  await addLibraryNote(datasetId,paper.id,id,content,anchored);
 }catch(error){
  // A proxy can return 502 after the transaction commits. The stable note ID
  // makes a retry safe; first check whether the write actually succeeded.
  try{
   const latest=await loadLibrary(datasetId);
   if(latest.data.notes.some(note=>note.id===id&&note.paperId===paper.id&&note.content===content))return;
  }catch{/* Surface the original save failure. */}
  throw error;
 }
}

export async function listPdfDocumentHighlights(datasetId:string,documentId:string):Promise<PdfNoteHighlight[]>{
 const library=await loadLibrary(datasetId);
 const {paper,attachment}=linkedDocument(library.data,documentId);
 const notes = library.data.notes.filter(note=>note.paperId===paper.id&&paper.noteIds.includes(note.id))
  .flatMap(note=>{
   const source=note.source;
   return source?.attachmentId===attachment.id&&source.rects?.length&&source.color
    ?[{id:note.id,page:source.page,color:source.color,rects:source.rects}]
    :[];
  });
 const annotations=(library.data.annotations||[]).filter(item=>item.paperId===paper.id&&item.attachmentId===attachment.id&&Number.isInteger(item.page)&&item.page!>0&&item.pdfRects?.length)
  .map(item=>({id:item.id,page:item.page!,color:item.color,rects:[],pdfRects:item.pdfRects}));
 return [...notes,...annotations];
}
