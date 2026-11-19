import {axiosInstance, BASE_URL} from '@/components/request';
import {pdfArtifactContentUrl, type PdfArtifact} from '../api/pdfArtifacts';
import {confirmLibraryPdf, decodePersistedLibrary} from './store';
import {createPaperPdfUploader} from './pdfUpload';

export async function fetchTranslationFile(datasetId:string,documentId:string,artifact:PdfArtifact):Promise<File>{
  const response=await axiosInstance.get(pdfArtifactContentUrl(datasetId,documentId,artifact.id),{responseType:'blob'});
  return new File([response.data],artifact.filename,{type:'application/pdf'});
}

export function createTranslationAttachmentSaver(datasetId:string,documentId:string,artifact:PdfArtifact){
  const upload=createPaperPdfUploader(datasetId);
  let file:File|undefined;
  let uploadedDocumentId:string|undefined;
  return async()=>{
    const response=await axiosInstance.get(`${BASE_URL}/api/core/academic/library`,{params:{target_dataset_id:datasetId,include_processing:false}});
    const library=decodePersistedLibrary(response.data);
    if(!library.canWrite)throw new Error('没有向该文献添加附件的权限');
    const ids=new Set(library.data.attachments.filter(a=>a.contentRef===documentId).map(a=>a.paperId));
    const paper=ids.size===1?library.data.papers.find(p=>ids.has(p.id)):undefined;
    if(!paper)throw new Error('当前文档尚未唯一关联文献，无法添加附件');
    const key=`translation-attachment:${artifact.id}`;
    if(!uploadedDocumentId){
      file ??= await fetchTranslationFile(datasetId,documentId,artifact);
      const uploaded=await upload(file,'UPLOAD',{idempotency_key:key,library_paper_id:paper.id,title:paper.metadata.title,paper});
      uploadedDocumentId=uploaded.documentId;
    }
    await confirmLibraryPdf(datasetId,paper,uploadedDocumentId,key);
  };
}
