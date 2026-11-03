import {fireEvent,render,screen} from '@testing-library/react';
import {expect,it,vi} from 'vitest';
import {PdfStudyDialog} from './PdfStudyDialog';

it('keeps study content in a portal over the PDF and closes with Escape or outside click',()=>{
 const close=vi.fn();
 const view=render(<main data-testid="reader"><PdfStudyDialog onClose={close}><button>符号</button></PdfStudyDialog></main>);
 const dialog=screen.getByRole('dialog',{name:'原文解读'});
 expect(view.container.contains(dialog)).toBe(false);
 expect(dialog).toContainElement(screen.getByRole('button',{name:'符号'}));
 fireEvent.keyDown(dialog,{key:'Escape'});
 expect(close).toHaveBeenCalledTimes(1);
 fireEvent.pointerDown(dialog.parentElement!);
 expect(close).toHaveBeenCalledTimes(2);
});

it('confines focus to visible dialog controls and restores original focus and background state',()=>{
 const opener=document.createElement('button');opener.textContent='Open';document.body.append(opener);opener.focus();
 const view=render(<PdfStudyDialog onClose={vi.fn()}><button>First</button><button>Last</button><details><summary>More</summary><button>Hidden detail</button></details><button disabled>Disabled</button></PdfStudyDialog>);
 const dialog=screen.getByRole('dialog');
 expect(view.container).toHaveAttribute('inert');
 const close=screen.getByRole('button',{name:'关闭原文解读'});const more=screen.getByText('More');
 more.focus();fireEvent.keyDown(more,{key:'Tab'});expect(close).toHaveFocus();
 fireEvent.keyDown(close,{key:'Tab',shiftKey:true});expect(more).toHaveFocus();
 dialog.focus();fireEvent.keyDown(dialog,{key:'Tab',shiftKey:true});expect(more).toHaveFocus();
 view.unmount();expect(view.container).not.toHaveAttribute('inert');expect(opener).toHaveFocus();opener.remove();
});
