import { describe, expect, it, vi, beforeEach } from 'vitest';
const mocks = vi.hoisted(() => ({ check: vi.fn(), upload: vi.fn(), hash: vi.fn() }));
vi.mock('./request', () => ({TaskServiceApi: () => ({checkHashes:mocks.check,uploadFiles:mocks.upload}), uploadLargeFileToDataset: vi.fn()}));
vi.mock('./fileHash', () => ({CHECK_HASHES_BATCH_SIZE:20,computeFileSha256:mocks.hash}));
import { buildUploadTaskItems } from './uploadByHash';
beforeEach(()=>{vi.resetAllMocks();mocks.hash.mockImplementation(async (f:File)=>f.name); mocks.check.mockImplementation(async (_:string,hashes:string[])=>({data:{missing_hashes:hashes}}));});
describe('partial uploads',()=>{
 it('continues after an upload fails, retaining input filenames and later files',async()=>{
  mocks.upload.mockImplementation(async (_:string,form:FormData)=>{const f=form.get('files') as File;if(f.name==='bad.txt')throw new Error('upload failed');return {data:{files:[{upload_file_id:f.name}]}}});
  const onFileError=vi.fn();
  const items=await buildUploadTaskItems({datasetId:'kb',fileItems:['first.txt','bad.txt','last.txt'].map(name=>({originFile:new File(['x'],name),path:name})),onFileError} as any);
  expect(items.map(x=>x.upload_file_id)).toEqual(['first.txt','last.txt']);
  expect(onFileError).toHaveBeenCalledWith(expect.objectContaining({path:'bad.txt'}),expect.objectContaining({message:'upload failed'}));
 });
 it('reports zero-byte files without hashing or uploading them and continues',async()=>{
  mocks.upload.mockResolvedValue({data:{files:[{upload_file_id:'good'}]}});
  const onFileError=vi.fn();
  const empty=new File([],'empty.txt'),good=new File([' '],'space.txt');
  const items=await buildUploadTaskItems({datasetId:'kb',fileItems:[{originFile:empty,path:'empty.txt'},{originFile:good,path:'space.txt'}],onFileError} as any);
  expect(items).toHaveLength(1);expect(mocks.hash).toHaveBeenCalledTimes(1);
  expect(onFileError.mock.calls[0][1].message).toContain('空文件');
 });
});
