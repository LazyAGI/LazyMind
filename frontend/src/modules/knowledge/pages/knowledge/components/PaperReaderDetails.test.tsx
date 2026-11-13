import {act,fireEvent,render,screen,waitFor,cleanup,within} from '@testing-library/react';
import {afterEach,beforeEach,expect,it,vi} from 'vitest';
import PaperReaderDetails from './PaperReaderDetails';
vi.mock('react-i18next',()=>({useTranslation:()=>({i18n:{language:'en-US'}})}));
const api=vi.hoisted(()=>({get:vi.fn(),add:vi.fn(),remove:vi.fn(),edit:vi.fn()}));
vi.mock('@/components/request',()=>({BASE_URL:'',axiosInstance:{get:api.get}}));
vi.mock('@/modules/knowledge/paper/store',async()=>{const original=await vi.importActual<any>('@/modules/knowledge/paper/store');return {...original,addLibraryNote:api.add,deleteLibraryNote:api.remove,updateLibraryNote:api.edit};});
const library=()=>({can_write:true,paper_count:1,pdf_attachment_count:1,active:true,data:{folders:[],papers:[{id:'paper',revision:1,metadata:{title:'Real {paper} & result',authors:['Ada Example'],year:2025,venue:'Test journal',abstract:'A real abstract.',identifiers:{doi:'10.1234/example'}},tags:[],attachmentIds:['a'],noteIds:['n']}],attachments:[{id:'a',paperId:'paper',contentRef:'doc'}],notes:[{id:'n',paperId:'paper',title:'Earlier note',content:'Read the evaluation.',createdAt:'2026-01-01'}]}});
beforeEach(()=>{api.get.mockReset().mockResolvedValue({data:library()});api.add.mockReset();api.remove.mockReset();api.edit.mockReset();vi.spyOn(window,'confirm').mockReturnValue(true);Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:vi.fn().mockResolvedValue(undefined)}});});
afterEach(cleanup);
const props={datasetId:'dataset',documentId:'doc',canWrite:true,fileName:'source.pdf'};
it('reads server metadata without migration and copies escaped BibTeX',async()=>{
 render(<PaperReaderDetails {...props}/>);expect(await screen.findByText('Real {paper} & result')).toBeInTheDocument();
 expect(api.get).toHaveBeenCalledWith('/api/core/academic/library',expect.objectContaining({params:{target_dataset_id:'dataset',include_processing:false},signal:expect.any(AbortSignal)}));
 fireEvent.click(screen.getByRole('button',{name:'Copy BibTeX'}));
 await waitFor(()=>expect(navigator.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining('Real \\{paper\\} \\& result')));
 expect(screen.getByRole('link',{name:'10.1234/example'})).toHaveAttribute('href','https://doi.org/10.1234/example');
});
it('retains note and id on failure then renders authoritative saved note',async()=>{
 api.add.mockRejectedValueOnce(new Error('Storage offline'));const saved=library().data;saved.notes.push({id:'new',paperId:'paper',title:'New note',content:'Keep this insight',createdAt:'2026-01-02'});saved.papers[0].noteIds.push('new');api.add.mockResolvedValueOnce({data:saved});
 render(<PaperReaderDetails {...props} initialTab="notes"/>);await screen.findByText('Read the evaluation.');
 fireEvent.click(screen.getByText('Write a note'));
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
 fireEvent.click(screen.getByText('Write a note'));
 fireEvent.change(screen.getByRole('textbox',{name:'New note'}),{target:{value:'Old request'}});fireEvent.click(screen.getByRole('button',{name:'Save note'}));
 view.rerender(<PaperReaderDetails {...props} documentId="other" initialTab="notes"/>);await screen.findByText(/not linked to a paper/);
 view.rerender(<PaperReaderDetails {...props} initialTab="notes"/>);await screen.findByText('Read the evaluation.');
 fireEvent.click(screen.getByText('Write a note'));
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

it('switches accessible notes and vocabulary tabs without losing either draft',async()=>{
 const view=render(<PaperReaderDetails {...props} initialTab="notes" documentVocabulary={<input aria-label="Vocabulary filter" defaultValue="saved word"/>}/>);
 await screen.findByText('Read the evaluation.');
 fireEvent.click(screen.getByText('Write a note'));
 const draft=screen.getByRole('textbox',{name:'New note'});
 fireEvent.change(draft,{target:{value:'Unfinished insight'}});
 const notesTab=screen.getByRole('tab',{name:'Notes'});
 const vocabularyTab=screen.getByRole('tab',{name:'Vocabulary'});
 expect(notesTab).toHaveAttribute('aria-selected','true');
 fireEvent.click(vocabularyTab);
 expect(screen.getByRole('tabpanel',{name:'Vocabulary'})).toBeVisible();
 expect(draft).not.toBeVisible();
 const filter=screen.getByRole('textbox',{name:'Vocabulary filter'});
 fireEvent.change(filter,{target:{value:'attention'}});
 fireEvent.keyDown(vocabularyTab,{key:'ArrowLeft'});
 expect(notesTab).toHaveFocus();
 expect(screen.getByRole('tabpanel',{name:'Notes'})).toBeVisible();
 expect(draft).toHaveValue('Unfinished insight');
 expect(draft).toBeVisible();
 fireEvent.click(vocabularyTab);
 expect(filter).toHaveValue('attention');
 view.rerender(<PaperReaderDetails {...props} initialTab="notes" documentVocabulary={<input aria-label="Vocabulary filter"/>} selectedNoteId="n"/>);
 expect(notesTab).toHaveAttribute('aria-selected','true');
 expect(screen.getByText('Read the evaluation.').closest('li')).toHaveFocus();
});
it.each(['unlinked','unavailable'])('shows vocabulary when paper metadata is %s',async(mode)=>{
 const value=library();value.data.attachments=[];
 if(mode==='unavailable')api.get.mockRejectedValue(new Error('Metadata unavailable'));else api.get.mockResolvedValue({data:value});
 render(<PaperReaderDetails {...props} initialTab="notes" documentVocabulary={<input aria-label="Vocabulary filter"/>}/>);
 await screen.findByText(mode==='unavailable'?'Metadata unavailable':/not linked to a paper/);
 fireEvent.click(screen.getByRole('tab',{name:'Vocabulary'}));
 expect(screen.getByRole('textbox',{name:'Vocabulary filter'})).toBeVisible();
 expect(screen.queryByRole('alert')).toBeNull();
});
it('keeps Notes only when vocabulary is disabled',async()=>{
 render(<PaperReaderDetails {...props} initialTab="notes"/>);
 await screen.findByText('Read the evaluation.');
 expect(screen.queryByRole('tab',{name:'Vocabulary'})).toBeNull();
 expect(screen.getByRole('heading',{name:'Notes'})).toBeVisible();
});
it('labels damaged imported notes and keeps their original text available',async()=>{
 const data=library();data.data.notes[0].title='???????? PDF';data.data.notes[0].content='???????????????? PDF ?????????????';api.get.mockResolvedValue({data});
 render(<PaperReaderDetails {...props} initialTab="notes"/>);
 expect(await screen.findByText('Imported note text is damaged')).toBeInTheDocument();
 expect(screen.getByText('View original text').closest('details')).not.toHaveAttribute('open');
 expect(screen.getByText('???????????????? PDF ?????????????')).toBeInTheDocument();
});
it('does not duplicate an auto-generated title that repeats the note body',async()=>{
 const data=library();data.data.notes[0].title='Read the evaluation.';api.get.mockResolvedValue({data});
 render(<PaperReaderDetails {...props} initialTab="notes"/>);
 expect(await screen.findAllByText('Read the evaluation.')).toHaveLength(1);
});
it('shows written note content without displaying its captured source quote',async()=>{
 const data=library() as any;
 data.data.notes[0]={...data.data.notes[0],title:'新笔记',content:'> Original PDF quote\n> Continued source text (第 3 页)\n\nMy own interpretation'};
 data.data.annotations=[{id:'annotation',paperId:'paper',quote:'Another original quote',comment:'My annotation',page:3}];
 api.get.mockResolvedValue({data});
 const onNavigatePage=vi.fn();render(<PaperReaderDetails {...props} initialTab="notes" onNavigatePage={onNavigatePage}/>);
 const writtenNote=await screen.findByRole('button',{name:/My own interpretation/});
 fireEvent.click(writtenNote);expect(onNavigatePage).toHaveBeenCalledWith(3);
 expect(screen.getByText('My annotation')).toBeInTheDocument();
 expect(screen.queryByText(/Original PDF quote/)).not.toBeInTheDocument();
 expect(screen.queryByText(/Continued source text/)).not.toBeInTheDocument();
 expect(screen.getByText('Another original quote')).toBeInTheDocument();
 expect(screen.queryByText('新笔记')).not.toBeInTheDocument();
});
it('keeps source location in saved content while editing only the note text',async()=>{
 const draftRequest={documentId:'doc',requestId:17,text:'My note',sourceQuote:'> Original PDF quote (第 4 页)'};
 api.add.mockResolvedValue({data:library().data});
 render(<PaperReaderDetails {...props} initialTab="notes" draftRequest={draftRequest}/>);
 const draft=await screen.findByRole('textbox',{name:'New note'});
 expect(draft).toHaveValue('My note');
 fireEvent.click(screen.getByRole('button',{name:'Save note'}));
 await waitFor(()=>expect(api.add).toHaveBeenCalledWith('dataset','paper',expect.any(String),'> Original PDF quote (第 4 页)\n\nMy note'));
});
it('preserves a manually written Markdown quote without source metadata',async()=>{
 const data=library();data.data.notes[0].content='> My own quoted idea\n\nHere is why';api.get.mockResolvedValue({data});
 render(<PaperReaderDetails {...props} initialTab="notes"/>);
 expect((await screen.findByText(/My own quoted idea/)).textContent).toBe('> My own quoted idea\n\nHere is why');
});
it('shows saved selection notes newest first, with their color and precise PDF jump',async()=>{
 const data=library() as any;
 const source={attachmentId:'a',page:3,rects:[[0.12,0.23,0.64,0.26]],bbox:[24,48,128,56],quote:'Original PDF sentence',color:'blue'};
 data.data.notes=[data.data.notes[0],{id:'recent',paperId:'paper',title:'新笔记',content:'This design is the key insight.',kind:'note',createdAt:'2026-01-02T00:00:00Z',updatedAt:'2026-01-02T00:00:00Z',source}];
 data.data.papers[0].noteIds=['n','recent'];api.get.mockResolvedValue({data});
 const onNavigateNote=vi.fn();render(<PaperReaderDetails {...props} initialTab="notes" onNavigateNote={onNavigateNote} documentVocabulary={<p>Vocabulary entries</p>}/>);
 const list=await screen.findByRole('list',{name:'Saved notes'});
 const entries=within(list).getAllByRole('listitem');
 expect(within(entries[0]).getByText('This design is the key insight.')).toBeInTheDocument();
 expect(within(entries[0]).getByText('Page 3')).toBeInTheDocument();
 expect(within(entries[0]).getByLabelText('Blue')).toBeInTheDocument();
 expect(screen.queryByText('Original PDF sentence')).not.toBeInTheDocument();
 fireEvent.click(screen.getByRole('tab',{name:'Vocabulary'}));
 fireEvent.click(screen.getByRole('tab',{name:'Notes'}));
 fireEvent.click(within(entries[0]).getByRole('button',{name:/This design is the key insight/}));
 expect(onNavigateNote).toHaveBeenCalledWith(source);
});
it('focuses a clicked highlight note and removes it from the saved list',async()=>{
 const data=library() as any;
 data.data.notes[0].source={attachmentId:'a',page:2,color:'yellow',rects:[[.1,.2,.3,.24]]};
 const after=library() as any;after.data.notes=[];after.data.papers[0].noteIds=[];
 api.get.mockResolvedValue({data});api.remove.mockResolvedValue({data:after.data});
 render(<PaperReaderDetails {...props} initialTab="notes" selectedNoteId="n" documentVocabulary={<p>Vocabulary entries</p>}/>);
 const entry=(await screen.findByText('Read the evaluation.')).closest('li')!;
 expect(entry).toHaveClass('is-selected');
 fireEvent.click(screen.getByRole('tab',{name:'Vocabulary'}));
 fireEvent.click(screen.getByRole('tab',{name:'Notes'}));
 fireEvent.click(within(entry).getByRole('button',{name:'Delete note'}));
 await waitFor(()=>expect(api.remove).toHaveBeenCalledWith('dataset','paper','n'));
 await waitFor(()=>expect(screen.queryByText('Read the evaluation.')).not.toBeInTheDocument());
});
it('refreshes the saved note list after an external popup save without replacing the panel',async()=>{
 const first=library() as any;const second=library() as any;
 second.data.notes.push({id:'new',paperId:'paper',title:'新笔记',content:'Saved in the selection popup',kind:'note',createdAt:'2026-01-02',updatedAt:'2026-01-02'});
 second.data.papers[0].noteIds.push('new');
 api.get.mockResolvedValueOnce({data:first}).mockResolvedValueOnce({data:second});
 const view=render(<PaperReaderDetails {...props} initialTab="notes" refreshToken={0}/>);
 await screen.findByText('Read the evaluation.');
 view.rerender(<PaperReaderDetails {...props} initialTab="notes" refreshToken={1}/>);
 expect(await screen.findByText('Saved in the selection popup')).toBeInTheDocument();
 expect(api.get).toHaveBeenCalledTimes(2);
});
it('does not jump to the wrong PDF when a note belongs to another attachment of the paper',async()=>{
 const data=library() as any;
 data.data.notes[0]={...data.data.notes[0],content:'Thought about the supplement',source:{attachmentId:'supplement',page:7,color:'green'}};
 api.get.mockResolvedValue({data});
 const onNavigateNote=vi.fn(),onNavigatePage=vi.fn();
 render(<PaperReaderDetails {...props} initialTab="notes" onNavigateNote={onNavigateNote} onNavigatePage={onNavigatePage}/>);
 const entry=(await screen.findByText('Thought about the supplement')).closest('li')!;
 expect(within(entry).getByText('Another attachment')).toBeInTheDocument();
 expect(within(entry).queryByRole('button',{name:/Thought about the supplement/})).toBeNull();
 expect(onNavigateNote).not.toHaveBeenCalled();expect(onNavigatePage).not.toHaveBeenCalled();
});
it('keeps imported PDF annotations compact, color coded and clickable as a whole entry',async()=>{
 const data=library() as any;
 data.data.annotations=[{id:'mark',paperId:'paper',attachmentId:'a',comment:'Check this result',quote:'PDF quote',page:5,color:'green'}];
 api.get.mockResolvedValue({data});
 const onNavigatePage=vi.fn();render(<PaperReaderDetails {...props} initialTab="notes" onNavigatePage={onNavigatePage}/>);
 const button=await screen.findByRole('button',{name:/Check this result/});
 expect(within(button).getByLabelText('Green')).toBeInTheDocument();
 expect(within(button).getByText('PDF quote')).toBeInTheDocument();
 fireEvent.click(button);expect(onNavigatePage).toHaveBeenCalledWith(5);
});
it('shows saved notes first and opens the manual editor only when requested',async()=>{
 render(<PaperReaderDetails {...props} initialTab="notes"/>);
 await screen.findByText('Read the evaluation.');
 const editor=screen.getByText('Write a note').closest('details')!;
 expect(editor).not.toHaveAttribute('open');
 fireEvent.click(screen.getByText('Write a note'));
 expect(editor).toHaveAttribute('open');
 expect(screen.getByRole('textbox',{name:'New note'})).toBeVisible();
});
it('shows a saved color even when the note has no usable page and avoids a broken jump',async()=>{
 const data=library() as any;
 data.data.notes[0]={...data.data.notes[0],source:{attachmentId:'a',color:'purple',quote:'Quote without page'}};
 data.data.annotations=[{id:'mark',paperId:'paper',attachmentId:'a',comment:'Imported mark without page',color:'red'}];
 api.get.mockResolvedValue({data});
 const onNavigateNote=vi.fn();render(<PaperReaderDetails {...props} initialTab="notes" onNavigateNote={onNavigateNote}/>);
 const entry=(await screen.findByText('Read the evaluation.')).closest('li')!;
 expect(within(entry).getByLabelText('Purple')).toBeInTheDocument();
 expect(within(entry).queryByRole('button',{name:/Read the evaluation/})).toBeNull();
 const imported=(await screen.findByText('Imported mark without page')).closest('li')!;
 expect(within(imported).getByLabelText('Red')).toBeInTheDocument();
 expect(within(imported).queryByRole('button')).toBeNull();
});

it('edits a saved note while preserving its anchor and separate new-note draft across tabs and retry',async()=>{
 const value=library();const source={attachmentId:'a',page:3,quote:'Evidence',color:'yellow',rects:[[.1,.2,.4,.3]]};Object.assign(value.data.notes[0],{source});api.get.mockResolvedValue({data:value});
 api.edit.mockRejectedValueOnce(new Error('Edit offline')).mockResolvedValueOnce({data:{...value.data,notes:[{...value.data.notes[0],content:'Updated insight'}]}});
 const navigate=vi.fn();render(<PaperReaderDetails {...props} initialTab="notes" onNavigateNote={navigate} documentVocabulary={<p>Words</p>}/>);
 await screen.findByText('Read the evaluation.');fireEvent.click(screen.getByText('Write a note'));fireEvent.change(screen.getByRole('textbox',{name:'New note'}),{target:{value:'Separate draft'}});
 fireEvent.click(screen.getByRole('button',{name:'Edit note'}));fireEvent.change(screen.getByRole('textbox',{name:'Edit note'}),{target:{value:'Updated insight'}});
 fireEvent.click(screen.getByRole('tab',{name:'Vocabulary'}));fireEvent.click(screen.getByRole('tab',{name:'Notes'}));expect(screen.getByRole('textbox',{name:'Edit note'})).toHaveValue('Updated insight');
 fireEvent.click(screen.getByRole('button',{name:'Save changes'}));await screen.findByText('Edit offline');expect(screen.getByRole('textbox',{name:'Edit note'})).toHaveValue('Updated insight');
 fireEvent.click(screen.getByRole('button',{name:'Save changes'}));await waitFor(()=>expect(screen.queryByRole('textbox',{name:'Edit note'})).not.toBeInTheDocument());
 expect(api.edit).toHaveBeenLastCalledWith('dataset','paper','n','Updated insight');expect(screen.getByRole('textbox',{name:'New note'})).toHaveValue('Separate draft');
 fireEvent.click(screen.getByRole('button',{name:/Updated insight/}));expect(navigate).toHaveBeenCalledWith(source);
});

it('preserves legacy quote location when editing and lets cancellation discard only the edit draft',async()=>{
 const value=library();value.data.notes[0].content='> Evidence (page 4)\n\nMy thought';api.get.mockResolvedValue({data:value});api.edit.mockResolvedValue({data:value.data});
 render(<PaperReaderDetails {...props} initialTab="notes"/>);await screen.findByText('My thought');
 fireEvent.click(screen.getByRole('button',{name:'Edit note'}));expect(screen.getByRole('textbox',{name:'Edit note'})).toHaveValue('My thought');
 fireEvent.change(screen.getByRole('textbox',{name:'Edit note'}),{target:{value:'Discard'}});fireEvent.click(screen.getByRole('button',{name:'Cancel editing'}));expect(api.edit).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole('button',{name:'Edit note'}));expect(screen.getByRole('textbox',{name:'Edit note'})).toHaveValue('My thought');fireEvent.change(screen.getByRole('textbox',{name:'Edit note'}),{target:{value:'Revised'}});fireEvent.click(screen.getByRole('button',{name:'Save changes'}));
 await waitFor(()=>expect(api.edit).toHaveBeenCalledWith('dataset','paper','n','> Evidence (page 4)\n\nRevised'));
});

it('ignores pending edit completion after switching documents',async()=>{
 let complete!:(value:any)=>void;api.edit.mockImplementation(()=>new Promise(resolve=>complete=resolve));
 const view=render(<PaperReaderDetails {...props} initialTab="notes"/>);await screen.findByText('Read the evaluation.');fireEvent.click(screen.getByRole('button',{name:'Edit note'}));fireEvent.change(screen.getByRole('textbox',{name:'Edit note'}),{target:{value:'Old edit'}});fireEvent.click(screen.getByRole('button',{name:'Save changes'}));
 view.rerender(<PaperReaderDetails {...props} documentId="other" initialTab="notes"/>);await screen.findByText(/not linked to a paper/);
 await act(async()=>complete({data:library().data}));expect(screen.queryByText('Old edit')).not.toBeInTheDocument();expect(screen.getByText(/not linked to a paper/)).toBeVisible();
});
