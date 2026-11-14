import {beforeEach,expect,it,vi} from 'vitest';
import {listPdfDocumentHighlights,savePdfSelectionNote} from './selectionNotes';

const api=vi.hoisted(()=>({get:vi.fn(),add:vi.fn()}));
vi.mock('@/components/request',()=>({BASE_URL:'',axiosInstance:{get:api.get}}));
vi.mock('./store',async()=>({...await vi.importActual<typeof import('./store')>('./store'),addLibraryNote:api.add}));

const savedLibrary=()=>({can_write:true,paper_count:1,pdf_attachment_count:2,active:true,data:{
 folders:[],papers:[{id:'paper',revision:1,metadata:{title:'Paper',authors:[],year:0,identifiers:{}},tags:[],attachmentIds:['pdf-a','pdf-b'],noteIds:[] as string[]}],
 attachments:[{id:'pdf-a',paperId:'paper',contentRef:'doc-a',isPrimary:true},{id:'pdf-b',paperId:'paper',contentRef:'doc-b'}],notes:[] as Array<{id:string;paperId:string;content:string;source?:unknown}>,
}});
beforeEach(()=>{api.get.mockReset().mockResolvedValue({data:savedLibrary()});api.add.mockReset().mockResolvedValue({data:savedLibrary().data});});

it('persists note text, color and exact PDF anchor on the linked attachment',async()=>{
 const source={page:3,rects:[[.1,.2,.3,.24] as [number,number,number,number]],bbox:[72,144,216,173] as [number,number,number,number],quote:'selected words',color:'blue' as const};
 await savePdfSelectionNote('dataset','doc-a','My interpretation',source,'note-id');
 expect(api.get).toHaveBeenCalledWith('/api/core/academic/library',expect.objectContaining({params:{target_dataset_id:'dataset',include_processing:false}}));
 expect(api.add).toHaveBeenCalledWith('dataset','paper','note-id','My interpretation',{...source,attachmentId:'pdf-a'});
});

it('checks whether an idempotent note was written when the save response fails',async()=>{
 const source={page:1,quote:'quote',color:'yellow' as const};
 api.add.mockRejectedValueOnce(new Error('Request failed with status code 502'));
 const after=savedLibrary();after.data.papers[0].noteIds.push('same-id');
 after.data.notes.push({id:'same-id',paperId:'paper',content:'Thought',source:{...source,attachmentId:'pdf-a'}});
 api.get.mockResolvedValueOnce({data:savedLibrary()}).mockResolvedValueOnce({data:after});
 await expect(savePdfSelectionNote('dataset','doc-a','Thought',source,'same-id')).resolves.toBeUndefined();
 expect(api.get).toHaveBeenCalledTimes(2);
});

it('does not write a note to an unrelated paper when an attachment is missing',async()=>{
 await expect(savePdfSelectionNote('dataset','missing','Thought',{page:1,color:'yellow'},'id')).rejects.toThrow(/关联/);
 expect(api.add).not.toHaveBeenCalled();
});

it('restores only highlights saved for this PDF from the paper library',async()=>{
 const value=savedLibrary();value.data.papers[0].noteIds.push('one','other');
 value.data.notes.push({id:'one',paperId:'paper',content:'One',source:{attachmentId:'pdf-a',page:2,color:'green',rects:[[.2,.3,.4,.35]]}});
 value.data.notes.push({id:'other',paperId:'paper',content:'Other',source:{attachmentId:'pdf-b',page:3,color:'red',rects:[[.2,.3,.4,.35]]}});
 api.get.mockResolvedValue({data:value});
 await expect(listPdfDocumentHighlights('dataset','doc-a')).resolves.toEqual([{id:'one',page:2,color:'green',rects:[[.2,.3,.4,.35]]}]);
});

it('restores imported Zotero geometry only for the current paper attachment',async()=>{
 const value=savedLibrary() as any;
 value.data.annotations=[
  {id:'zotero',paperId:'paper',attachmentId:'pdf-a',page:2,color:'yellow',quote:'Source text',pdfRects:[[72,700,200,716]]},
  {id:'other',paperId:'paper',attachmentId:'pdf-b',page:2,color:'yellow',pdfRects:[[72,700,200,716]]},
  {id:'unbound',paperId:'paper',page:2,color:'yellow',pdfRects:[[72,700,200,716]]},
 ];
 api.get.mockResolvedValue({data:value});
 await expect(listPdfDocumentHighlights('dataset','doc-a')).resolves.toEqual([{id:'zotero',page:2,color:'yellow',rects:[],pdfRects:[[72,700,200,716]]}]);
});
