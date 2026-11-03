import {act,fireEvent,render,screen,waitFor,cleanup} from '@testing-library/react';
import {afterEach,beforeEach,expect,it,vi} from 'vitest';
import PaperReaderDetails from './PaperReaderDetails';
vi.mock('react-i18next',()=>({useTranslation:()=>({i18n:{language:'en-US'}})}));
const api=vi.hoisted(()=>({get:vi.fn(),add:vi.fn()}));
vi.mock('@/components/request',()=>({BASE_URL:'',axiosInstance:{get:api.get}}));
vi.mock('@/modules/knowledge/paper/store',async()=>{const original=await vi.importActual<any>('@/modules/knowledge/paper/store');return {...original,addLibraryNote:api.add};});
const library=()=>({can_write:true,paper_count:1,pdf_attachment_count:1,active:true,data:{folders:[],papers:[{id:'paper',revision:1,metadata:{title:'Real {paper} & result',authors:['Ada Example'],year:2025,venue:'Test journal',abstract:'A real abstract.',identifiers:{doi:'10.1234/example'}},tags:[],attachmentIds:['a'],noteIds:['n']}],attachments:[{id:'a',paperId:'paper',contentRef:'doc'}],notes:[{id:'n',paperId:'paper',title:'Earlier note',content:'Read the evaluation.',createdAt:'2026-01-01'}]}});
beforeEach(()=>{api.get.mockReset().mockResolvedValue({data:library()});api.add.mockReset();Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:vi.fn().mockResolvedValue(undefined)}});});
afterEach(cleanup);
const props={datasetId:'dataset',documentId:'doc',canWrite:true,fileName:'source.pdf'};
it('reads server metadata without migration and copies escaped BibTeX',async()=>{
 render(<PaperReaderDetails {...props}/>);expect(await screen.findByText('Real {paper} & result')).toBeInTheDocument();
 expect(api.get).toHaveBeenCalledWith('/api/core/academic/library',expect.objectContaining({params:{target_dataset_id:'dataset'},signal:expect.any(AbortSignal)}));
 fireEvent.click(screen.getByRole('button',{name:'Copy BibTeX'}));
 await waitFor(()=>expect(navigator.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining('Real \\{paper\\} \\& result')));
 expect(screen.getByRole('link',{name:'10.1234/example'})).toHaveAttribute('href','https://doi.org/10.1234/example');
});
it('retains note and id on failure then renders authoritative saved note',async()=>{
 api.add.mockRejectedValueOnce(new Error('Storage offline'));const saved=library().data;saved.notes.push({id:'new',paperId:'paper',title:'New note',content:'Keep this insight',createdAt:'2026-01-02'});saved.papers[0].noteIds.push('new');api.add.mockResolvedValueOnce({data:saved});
 render(<PaperReaderDetails {...props} initialTab="notes"/>);await screen.findByText('Read the evaluation.');
 fireEvent.change(screen.getByRole('textbox',{name:'New note'}),{target:{value:'Keep this insight'}});fireEvent.click(screen.getByRole('button',{name:'Save note'}));
 await screen.findByText('Storage offline');expect(screen.getByRole('textbox',{name:'New note'})).toHaveValue('Keep this insight');
 fireEvent.click(screen.getByRole('button',{name:'Save note'}));await screen.findByText('Keep this insight');
 expect(api.add.mock.calls[0]).toEqual(api.add.mock.calls[1]);expect(screen.getByRole('textbox',{name:'New note'})).toHaveValue('');
});
it.each([false,true])('requires both parent and server write access (parent %s)',async(parent)=>{
 const value=library();value.can_write=!parent;api.get.mockResolvedValue({data:value});render(<PaperReaderDetails {...props} canWrite={parent} initialTab="notes"/>);await screen.findByText('Read the evaluation.');expect(screen.queryByRole('button',{name:'Save note'})).not.toBeInTheDocument();
});
it('does not infer a paper from filename when the attachment mapping is missing',async()=>{
 const value=library();value.data.attachments=[];api.get.mockResolvedValue({data:value});render(<PaperReaderDetails {...props} initialTab="notes"/>);await screen.findByText(/not linked to a paper/);expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
});
it('ignores an old library response after switching documents',async()=>{
 let resolve!:(value:any)=>void;api.get.mockImplementationOnce(()=>new Promise(r=>resolve=r));const next=library();next.data.attachments=[];api.get.mockResolvedValueOnce({data:next});const view=render(<PaperReaderDetails {...props}/>);view.rerender(<PaperReaderDetails {...props} documentId="other"/>);await screen.findByText('source.pdf');await act(async()=>resolve({data:library()}));expect(screen.queryByText('Real {paper} & result')).not.toBeInTheDocument();
});
it('does not apply an earlier note save after switching away and back to a document',async()=>{
 let complete!:(value:any)=>void;api.add.mockImplementation(()=>new Promise(r=>complete=r));
 const view=render(<PaperReaderDetails {...props} initialTab="notes"/>);await screen.findByText('Read the evaluation.');
 fireEvent.change(screen.getByRole('textbox',{name:'New note'}),{target:{value:'Old request'}});fireEvent.click(screen.getByRole('button',{name:'Save note'}));
 view.rerender(<PaperReaderDetails {...props} documentId="other" initialTab="notes"/>);await screen.findByText(/not linked to a paper/);
 view.rerender(<PaperReaderDetails {...props} initialTab="notes"/>);await screen.findByText('Read the evaluation.');
 fireEvent.change(screen.getByRole('textbox',{name:'New note'}),{target:{value:'Current draft'}});
 await act(async()=>complete({data:library().data}));expect(screen.getByRole('textbox',{name:'New note'})).toHaveValue('Current draft');
});
it('keeps the quote draft supplied before metadata loads and appends without replacing an existing note',async()=>{
 const request={documentId:'doc',requestId:1,text:'> Quoted finding\n\nMy thought'};
 const view=render(<PaperReaderDetails {...props} initialTab="notes" draftRequest={request}/>);
 await screen.findByText('Read the evaluation.');
 expect(screen.getByRole('textbox',{name:'New note'})).toHaveValue(request.text);
 fireEvent.change(screen.getByRole('textbox',{name:'New note'}),{target:{value:'Unfinished note'}});
 view.rerender(<PaperReaderDetails {...props} initialTab="notes" draftRequest={{...request,requestId:2}}/>);
 expect(screen.getByRole('textbox',{name:'New note'})).toHaveValue('Unfinished note\n\n'+request.text);
 view.rerender(<PaperReaderDetails {...props} initialTab="notes" draftRequest={{...request,requestId:2}}/>);
 expect(screen.getByRole('textbox',{name:'New note'})).toHaveValue('Unfinished note\n\n'+request.text);
});
it('retains a captured note draft when retrying metadata after a network failure',async()=>{
 api.get.mockRejectedValueOnce(new Error('Metadata unavailable')).mockResolvedValueOnce({data:library()});
 render(<PaperReaderDetails {...props} initialTab="notes" draftRequest={{documentId:'doc',requestId:12,text:'> Source quote\n\nDo not lose this note'}}/>);
 await screen.findByText('Metadata unavailable');fireEvent.click(screen.getByRole('button',{name:'Retry'}));
 await screen.findByText('Read the evaluation.');expect(screen.getByRole('textbox',{name:'New note'})).toHaveValue('> Source quote\n\nDo not lose this note');
});

it('keeps document vocabulary accessible without a linked paper and retains it between info and notes',async()=>{
 const value=library();value.data.attachments=[];api.get.mockResolvedValue({data:value});
 const view=render(<PaperReaderDetails {...props} documentVocabulary={<input aria-label="Vocabulary filter" defaultValue="saved word"/>}/>);
 await screen.findByText(/not linked to a paper/);
 const section=screen.getByText('Vocabulary').closest('details')!;
 fireEvent.click(screen.getByText('Vocabulary'));section.open=true;
 const filter=screen.getByRole('textbox',{name:'Vocabulary filter'});
 view.rerender(<PaperReaderDetails {...props} initialTab="notes" documentVocabulary={<input aria-label="Vocabulary filter" defaultValue="saved word"/>}/>);
 expect(filter).not.toBeVisible();
 view.rerender(<PaperReaderDetails {...props} documentVocabulary={<input aria-label="Vocabulary filter" defaultValue="saved word"/>}/>);
 expect(filter).toBeVisible();expect(filter).toHaveValue('saved word');
 view.rerender(<PaperReaderDetails {...props}/>);
 expect(screen.queryByText('Vocabulary')).not.toBeInTheDocument();
});
