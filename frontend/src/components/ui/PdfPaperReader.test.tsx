import {act,fireEvent,render,screen,waitFor} from '@testing-library/react';
import {beforeAll,describe,it,expect,vi} from 'vitest';
import i18n from '@/i18n';
import {readablePaperText} from '@/modules/knowledge/api/paperReader';
import {PdfVisualStudy} from './PdfVisualStudy';
import {PdfBilingualReader} from './PdfBilingualReader';
const api=vi.hoisted(()=>({post:vi.fn()}));
vi.mock('@/components/request',()=>({axiosInstance:api,BASE_URL:''}));
const target={dataset:'d',document:'paper'};
const document={version:1 as const,source_sha256:'source',coordinate_space:'normalized_top_left' as const,parser:{name:'mineru',model:'v'},pages:[{index:0},{index:1}],blocks:[{id:'p1',type:'text',page:0,text:'Original <sup>1</sup> paragraph.'},{id:'p2',type:'text',page:1,text:'Next page text.'},{id:'fig',type:'image',page:0,text:'Figure caption.'}]};
beforeAll(async()=>{await i18n.changeLanguage('en-US');});
describe('paper reader modes',()=>{
 it('translates only requested paragraphs, pairs originals and cancels stale page requests',async()=>{
 let finish!:(value:unknown)=>void;
 api.post.mockReset().mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve})).mockResolvedValue({data:{data:{translated_text:'Actual translated text',source_sha256:'source',block_id:'p2',language:'zh'}}});
 const view=render(<PdfBilingualReader target={target} document={document} active onNavigate={vi.fn()} onStudy={vi.fn()}/>);
 expect(api.post).not.toHaveBeenCalled();
 expect(screen.getByText('Original 1 paragraph.')).toBeInTheDocument();
 fireEvent.click(screen.getByRole('button',{name:'Translate paragraph'}));
 const signal=api.post.mock.calls[0][2].signal;
 fireEvent.change(screen.getByRole('combobox',{name:'Page'}),{target:{value:'1'}});
 expect(signal.aborted).toBe(true);
 await act(async()=>finish({data:{data:{translated_text:'Stale translation',source_sha256:'source',block_id:'p1',language:'zh'}}}));
 expect(screen.queryByText('Stale translation')).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'Translate paragraph'}));
 expect(await screen.findByText('Actual translated text')).toBeInTheDocument();
 expect(screen.getByText('Next page text.')).toBeInTheDocument();
 view.unmount();
 });
 it('clears translations when source changes and shows actionable failures',async()=>{
 api.post.mockReset().mockRejectedValue(new Error('offline'));
 const view=render(<PdfBilingualReader target={target} document={document} active onNavigate={vi.fn()} onStudy={vi.fn()}/>);
 fireEvent.click(screen.getByRole('button',{name:'Translate paragraph'}));
 expect(await screen.findByRole('alert')).toHaveTextContent('Translation failed');
 view.rerender(<PdfBilingualReader target={target} document={{...document,source_sha256:'new'}} active onNavigate={vi.fn()} onStudy={vi.fn()}/>);
 await waitFor(()=>expect(screen.queryByRole('alert')).toBeNull());
 });
});


describe('reader grounding and resource limits',()=>{
 it('rejects a translation for the wrong source rather than displaying it',async()=>{
  api.post.mockReset().mockResolvedValue({data:{data:{block_id:'p1',language:'zh',source_sha256:'wrong',translated_text:'Untrusted stale result'}}});
  render(<PdfBilingualReader target={target} document={document} active onNavigate={vi.fn()} onStudy={vi.fn()}/>);
  fireEvent.click(screen.getByRole('button',{name:'Translate paragraph'}));
  expect(await screen.findByRole('alert')).toHaveTextContent('Translation failed');
  expect(screen.queryByText('Untrusted stale result')).toBeNull();
  expect(api.post.mock.calls[0][0]).toContain('/pdf-ir/blocks/p1/translation');
  expect(api.post.mock.calls[0][1]).toEqual({language:'zh',force:false,source_sha256:'source'});
 });
 it('keeps math intact while safely decoding textual HTML',()=>{
  expect(readablePaperText('Author <sup>1</sup> &amp; $a<b>c$')).toBe('Author 1 & $a<b>c$');
  expect(readablePaperText('<script>alert(1)</script><img src=x onerror=alert(1)>Text')).toBe('Text');
 });
 it('bounds page batches to two active requests and twenty paragraphs',async()=>{
  const pending:Array<()=>void>=[];
  api.post.mockReset().mockImplementation((url:string)=>new Promise(resolve=>{const id=url.split('/blocks/')[1].split('/')[0];pending.push(()=>resolve({data:{data:{block_id:id,language:'zh',source_sha256:'source',translated_text:'Translated '+id}}}));}));
  const many={...document,blocks:Array.from({length:22},(_,index)=>({id:'para'+index,type:'text',page:0,text:'Original '+index}))};
  render(<PdfBilingualReader target={target} document={many} active onNavigate={vi.fn()} onStudy={vi.fn()}/>);
  fireEvent.click(screen.getByRole('button',{name:'Translate this page'}));expect(api.post).toHaveBeenCalledTimes(2);
  for(let round=0;round<10;round++)await act(async()=>{pending.splice(0).forEach(finish=>finish());});
  expect(api.post).toHaveBeenCalledTimes(20);
  expect(screen.getAllByRole('button',{name:'Translate paragraph'})).toHaveLength(2);
 });
});
it('rejects visual explanations for a different source and never exposes their claims',async()=>{
 api.post.mockReset().mockResolvedValue({data:{data:{source_sha256:'other',summary:'Wrong visual claim',observations:[],evidence:[],warnings:[]}}});
 render(<PdfVisualStudy target={target} document={document} block={document.blocks[2]} onNavigate={vi.fn()} onInspectTable={vi.fn()} onClose={vi.fn()} onReturn={vi.fn()} canReturn={false}/>);
 expect(api.post.mock.calls[0][1]).toEqual({language:'en',force:false,source_sha256:'source'});
 await screen.findByRole('alert');expect(screen.queryByText('Wrong visual claim')).toBeNull();
});
