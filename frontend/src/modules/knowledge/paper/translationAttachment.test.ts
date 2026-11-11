import {beforeEach,expect,it,vi} from 'vitest';
import {libraryFixture,paperFixture} from './__tests__/fixtures';
import type {PdfArtifact} from '../api/pdfArtifacts';
const calls=vi.hoisted(()=>({get:vi.fn(),upload:vi.fn(),confirm:vi.fn()}));
vi.mock('@/components/request',()=>({BASE_URL:'',axiosInstance:{get:calls.get}}));
vi.mock('./pdfUpload',()=>({createPaperPdfUploader:()=>calls.upload}));
vi.mock('./store',async importOriginal=>({...await importOriginal<typeof import('./store')>(),confirmLibraryPdf:calls.confirm}));
import {createTranslationAttachmentSaver} from './translationAttachment';
const artifact={id:'translated',filename:'translated.pdf'} as PdfArtifact;
const paper=paperFixture();
beforeEach(()=>{
 vi.clearAllMocks();
 const data=libraryFixture({papers:[paper]});
 data.attachments[0].contentRef='source';
 calls.get.mockImplementation((url:string)=>Promise.resolve({data:url.endsWith('/library')?{data,can_write:true}:new Blob(['%PDF-test'])}));
 calls.upload.mockResolvedValue({documentId:'copy'});
 calls.confirm.mockResolvedValue({});
});
it('adds a separate document to the original paper and retries association without reuploading',async()=>{
 calls.confirm.mockRejectedValueOnce(new Error('network'));
 const save=createTranslationAttachmentSaver('dataset','source',artifact);
 await expect(save()).rejects.toThrow('network');
 await save();
 expect(calls.upload).toHaveBeenCalledTimes(1);
 expect(calls.upload).toHaveBeenCalledWith(expect.any(File),'UPLOAD',expect.objectContaining({library_paper_id:paper.id,idempotency_key:'translation-attachment:translated'}));
 expect(calls.confirm).toHaveBeenLastCalledWith('dataset',expect.objectContaining({id:paper.id}),'copy','translation-attachment:translated');
});
it('does not upload when the source is not associated with a paper',async()=>{
 await expect(createTranslationAttachmentSaver('dataset','unknown',artifact)()).rejects.toThrow('尚未唯一关联');
 expect(calls.upload).not.toHaveBeenCalled();
});
it('does not upload without library write permission',async()=>{
 calls.get.mockResolvedValue({data:{data:libraryFixture(),can_write:false}});
 await expect(createTranslationAttachmentSaver('dataset','source',artifact)()).rejects.toThrow('权限');
 expect(calls.upload).not.toHaveBeenCalled();
});
