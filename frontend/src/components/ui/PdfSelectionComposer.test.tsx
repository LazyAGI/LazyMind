import {fireEvent,render,screen} from '@testing-library/react';
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
it('passes a note draft and quote without claiming it has been saved',()=>{
 const note=vi.fn();const selection={text:'Quoted evidence',page:1};
 render(<PdfSelectionComposer selection={selection} onNote={note} onClose={vi.fn()}/>);
 fireEvent.change(screen.getByRole('textbox'),{target:{value:'My observation'}});
 fireEvent.click(screen.getByRole('button',{name:'添加笔记草稿'}));
 expect(note).toHaveBeenCalledWith(selection,'My observation');
 expect(screen.queryByText('已保存')).toBeNull();
});
it('shows a color palette in note mode and applies the selected highlight before opening a note draft',()=>{
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
 expect(note).toHaveBeenCalledWith(selection,'');
 expect(close).toHaveBeenCalledOnce();
});
it('replaces the note palette with a send action while composing',()=>{
 const note=vi.fn(),highlight=vi.fn();
 render(<PdfSelectionComposer selection={{text:'Quote',page:2}} onNote={note} onHighlight={highlight} onClose={vi.fn()}/>);
 fireEvent.change(screen.getByRole('textbox'),{target:{value:'My note'}});
 expect(screen.queryByRole('group',{name:'高亮颜色'})).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'添加笔记草稿'}));
 expect(highlight).toHaveBeenCalledWith('yellow');
 expect(note).toHaveBeenCalledWith({text:'Quote',page:2},'My note');
});
it('closes on Escape and exposes optional actions in a compact menu',()=>{
 const close=vi.fn(),action=vi.fn();render(<PdfSelectionComposer selection={{text:'quote',page:1}} onClose={close} actions={[{key:'reference',label:'打开链接',onClick:action}]}/>);
 fireEvent.click(screen.getByText('更多操作'));fireEvent.click(screen.getByRole('button',{name:'打开链接'}));expect(action).toHaveBeenCalledOnce();
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
 expect(screen.queryByText('更多操作')).toBeNull();
});
