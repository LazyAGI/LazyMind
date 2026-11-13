import {fireEvent,render,screen,waitFor} from '@testing-library/react';
import {expect,it,vi} from 'vitest';
import {PdfSelectionComposer} from './PdfSelectionComposer';

it('keeps the selected quote while editing and prepares a question without auto-sending',()=>{
 const ask=vi.fn(),close=vi.fn();const selection={text:'Source quote',page:2,context:'Source context'};
 render(<PdfSelectionComposer selection={selection} onAsk={ask} onClose={close}/>);
 const input=screen.getByRole('textbox',{name:'向 AI 提问或记笔记'});
 fireEvent.focus(input);fireEvent.change(input,{target:{value:'Why does this work?'}});
 expect(screen.queryByRole('button',{name:'快速解释'})).toBeNull();
 expect(screen.getByRole('button',{name:'向 AI 提问'})).toBeVisible();
 document.dispatchEvent(new Event('selectionchange'));
 expect(input).toHaveValue('Why does this work?');expect(close).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole('button',{name:'向 AI 提问'}));
 expect(ask).toHaveBeenCalledWith({...selection,question:'Why does this work?'});
 expect(close).toHaveBeenCalledOnce();
 expect(screen.queryByRole('button',{name:'下载并加入知识库'})).toBeNull();
});
it('saves a note directly from the selection popup and closes only after success',async()=>{
 let complete!:()=>void;
 const note=vi.fn().mockImplementation(()=>new Promise<void>(resolve=>{complete=resolve;}));const close=vi.fn();const selection={text:'Quoted evidence',page:1};
 render(<PdfSelectionComposer selection={selection} onNote={note} onClose={close}/>);
 fireEvent.change(screen.getByRole('textbox'),{target:{value:'My observation'}});
 fireEvent.click(screen.getByRole('button',{name:'保存笔记'}));
 expect(note).toHaveBeenCalledWith(selection,'My observation','yellow',expect.any(String));
 expect(close).not.toHaveBeenCalled();
 complete();await waitFor(()=>expect(close).toHaveBeenCalledOnce());
});
it('keeps note mode for the next text selection after switching modes',async()=>{
 const note=vi.fn().mockResolvedValue(undefined),ask=vi.fn(),close=vi.fn();
 let selectedMode:'ask'|'note'='ask';
 const onModeChange=(mode:'ask'|'note')=>{selectedMode=mode;};
 const {rerender}=render(<PdfSelectionComposer key="first" selection={{text:'First quote',page:1}} onAsk={ask} onNote={note} initialMode={selectedMode} onModeChange={onModeChange} onClose={close}/>);
 fireEvent.click(screen.getByRole('button',{name:'笔记'}));
 expect(selectedMode).toBe('note');
 rerender(<PdfSelectionComposer key="second" selection={{text:'Second quote',page:2}} onAsk={ask} onNote={note} initialMode={selectedMode} onModeChange={onModeChange} onClose={close}/>);
 fireEvent.change(screen.getByRole('textbox'),{target:{value:'Keep this insight'}});
 expect(screen.getByRole('button',{name:'保存笔记'})).toBeInTheDocument();
 fireEvent.click(screen.getByRole('button',{name:'保存笔记'}));
 await waitFor(()=>expect(note).toHaveBeenCalledWith({text:'Second quote',page:2},'Keep this insight','yellow',expect.any(String)));
 expect(ask).not.toHaveBeenCalled();
});
it('shows a color palette in note mode and can save a highlight without a note',()=>{
 const note=vi.fn(),highlight=vi.fn(),close=vi.fn();
 const selection={text:'Quoted evidence',page:1};
 render(<PdfSelectionComposer selection={selection} onAsk={vi.fn()} onNote={note} onHighlight={highlight} onClose={close}/>);
 expect(screen.queryByRole('group',{name:'高亮颜色'})).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'笔记'}));
 expect(screen.getByRole('group',{name:'高亮颜色'})).toBeInTheDocument();
 fireEvent.click(screen.getByRole('button',{name:'蓝色'}));
 expect(screen.getByRole('button',{name:'蓝色'})).toHaveAttribute('aria-pressed','true');
 fireEvent.click(screen.getByRole('button',{name:'高亮并记笔记'}));
 expect(highlight).toHaveBeenCalledWith('blue');
 expect(note).not.toHaveBeenCalled();
 expect(close).toHaveBeenCalledOnce();
});
it('keeps note colors available while composing and submits the chosen color',async()=>{
 const note=vi.fn().mockResolvedValue(undefined),highlight=vi.fn(),close=vi.fn();
 render(<PdfSelectionComposer selection={{text:'Quote',page:2}} onAsk={vi.fn()} onNote={note} onHighlight={highlight} onClose={close}/>);
 fireEvent.click(screen.getByRole('button',{name:'笔记'}));
 fireEvent.change(screen.getByRole('textbox'),{target:{value:'My note'}});
 expect(screen.getByRole('group',{name:'高亮颜色'})).toBeInTheDocument();
 fireEvent.click(screen.getByRole('button',{name:'蓝色'}));
 expect(screen.getByRole('button',{name:'蓝色'})).toHaveAttribute('aria-pressed','true');
 expect(screen.queryByRole('button',{name:'高亮并记笔记'})).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'保存笔记'}));
 expect(highlight).not.toHaveBeenCalled();
 expect(note).toHaveBeenCalledWith({text:'Quote',page:2},'My note','blue',expect.any(String));
 await waitFor(()=>expect(close).toHaveBeenCalledOnce());
});
it('retains the draft, color, and request id when saving fails so retry is safe',async()=>{
 const note=vi.fn().mockRejectedValueOnce(new Error('服务器暂时不可用')).mockResolvedValueOnce(undefined);const close=vi.fn();
 render(<PdfSelectionComposer selection={{text:'Quote',page:2}} onNote={note} onClose={close}/>);
 fireEvent.change(screen.getByRole('textbox'),{target:{value:'Important insight'}});
 fireEvent.click(screen.getByRole('button',{name:'蓝色'}));
 fireEvent.click(screen.getByRole('button',{name:'保存笔记'}));
 expect(await screen.findByRole('alert')).toHaveTextContent('服务器暂时不可用');
 expect(close).not.toHaveBeenCalled();expect(screen.getByRole('textbox')).toHaveValue('Important insight');
 fireEvent.click(screen.getByRole('button',{name:'保存笔记'}));
 await waitFor(()=>expect(close).toHaveBeenCalledOnce());
 expect(note.mock.calls[0][3]).toBe(note.mock.calls[1][3]);
});
it('uses a new note id if the draft changes after a failed save',async()=>{
 const note=vi.fn().mockRejectedValueOnce(new Error('暂时不可用')).mockResolvedValueOnce(undefined);
 const close=vi.fn();render(<PdfSelectionComposer selection={{text:'Quote',page:2}} onNote={note} onClose={close}/>);
 fireEvent.change(screen.getByRole('textbox'),{target:{value:'First version'}});
 fireEvent.click(screen.getByRole('button',{name:'保存笔记'}));
 await screen.findByRole('alert');
 fireEvent.change(screen.getByRole('textbox'),{target:{value:'Revised version'}});
 fireEvent.click(screen.getByRole('button',{name:'保存笔记'}));
 await waitFor(()=>expect(close).toHaveBeenCalledOnce());
 expect(note.mock.calls[0][3]).not.toBe(note.mock.calls[1][3]);
});
it('closes on Escape without exposing More or paragraph retranslation',()=>{
 const close=vi.fn(),action=vi.fn();render(<PdfSelectionComposer selection={{text:'quote',page:1}} onClose={close} actions={[{key:'reference',label:'打开链接',onClick:action},{key:'retranslate',label:'重译此段',onClick:action}]}/>);
 expect(screen.queryByText('更多操作')).toBeNull();expect(screen.queryByText('重译此段')).toBeNull();expect(action).not.toHaveBeenCalled();
 fireEvent.keyDown(screen.getByRole('textbox'),{key:'Escape'});expect(close).toHaveBeenCalled();
});

it('exposes quick explanation and vocabulary without opening More',()=>{
 const explain=vi.fn(),vocabulary=vi.fn();
 const {container}=render(<PdfSelectionComposer selection={{text:'attention',page:1}} onClose={vi.fn()} actions={[
  {key:'explanation',label:'快速解释',onClick:explain},
  {key:'vocabulary',label:'加入生词',onClick:vocabulary},
  {key:'reference',label:'打开链接',onClick:vi.fn()},
 ]}/>);
 for(const label of ['快速解释','加入生词'])expect(screen.getByRole('button',{name:label}).closest('details')).toBeNull();
 expect(container.querySelector('.anticon-bulb')).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'快速解释'}));expect(explain).toHaveBeenCalledOnce();
 fireEvent.click(screen.getByRole('button',{name:'加入生词'}));expect(vocabulary).toHaveBeenCalledOnce();
});
it('preserves disabled primary actions and hides an empty More menu',()=>{
 const action=vi.fn();render(<PdfSelectionComposer selection={{text:'word',page:1}} onClose={vi.fn()} actions={[{key:'explanation',label:'快速解释',disabled:true,title:'Unavailable',onClick:action}]}/>);
 expect(screen.getByRole('button',{name:'快速解释'})).toBeDisabled();
 expect(screen.getByRole('button',{name:'快速解释'})).toHaveAttribute('data-tooltip','快速解释选中文字 · Unavailable');
 expect(screen.queryByText('更多操作')).toBeNull();
});

it('exposes a visible tooltip label for each compact icon in ask and note modes',()=>{
 render(<PdfSelectionComposer selection={{text:'word',page:1}} onAsk={vi.fn()} onNote={vi.fn()} onTranslate={vi.fn()} translationDisabled translationTitle="请先配置翻译服务" onClose={vi.fn()} actions={[
  {key:'vocabulary',label:'加入生词',onClick:vi.fn()},
  {key:'explanation',label:'快速解释',onClick:vi.fn()},
 ]}/>);
 expect(screen.getByRole('button',{name:'关闭'})).toHaveAttribute('data-tooltip','关闭');
 expect(screen.getByRole('button',{name:'翻译'}).parentElement).toHaveAttribute('data-tooltip','翻译 · 请先配置翻译服务');
 expect(screen.getByRole('button',{name:'加入生词'})).toHaveAttribute('data-tooltip','加入生词');
 expect(screen.getByRole('button',{name:'快速解释'})).toHaveAttribute('data-tooltip','快速解释选中文字');
 fireEvent.click(screen.getByRole('button',{name:'笔记'}));
 expect(screen.getByRole('button',{name:'黄色'})).toHaveAttribute('data-tooltip','黄色');
 expect(screen.getByRole('button',{name:'高亮并记笔记'})).toHaveAttribute('data-tooltip','高亮并记笔记');
 fireEvent.change(screen.getByRole('textbox'),{target:{value:'A note'}});
 expect(screen.getByRole('button',{name:'保存笔记'})).toHaveAttribute('data-tooltip','保存笔记');
});
