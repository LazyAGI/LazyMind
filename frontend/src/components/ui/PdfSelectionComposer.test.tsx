import {fireEvent,render,screen} from '@testing-library/react';
import {expect,it,vi} from 'vitest';
import {PdfSelectionComposer} from './PdfSelectionComposer';

it('keeps the selected quote while editing and prepares a question without auto-sending',()=>{
 const ask=vi.fn(),close=vi.fn();const selection={text:'Source quote',page:2,context:'Source context'};
 render(<PdfSelectionComposer selection={selection} onAsk={ask} onClose={close}/>);
 const input=screen.getByRole('textbox',{name:'向 AI 提问或记笔记'});
 fireEvent.focus(input);fireEvent.change(input,{target:{value:'Why does this work?'}});
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
it('closes on Escape and exposes optional actions in a compact menu',()=>{
 const close=vi.fn(),action=vi.fn();render(<PdfSelectionComposer selection={{text:'quote',page:1}} onClose={close} actions={[{key:'vocabulary',label:'加入生词',onClick:action}]}/>);
 fireEvent.click(screen.getByText('更多操作'));fireEvent.click(screen.getByRole('button',{name:'加入生词'}));expect(action).toHaveBeenCalledOnce();
 fireEvent.keyDown(screen.getByRole('textbox'),{key:'Escape'});expect(close).toHaveBeenCalled();
});
