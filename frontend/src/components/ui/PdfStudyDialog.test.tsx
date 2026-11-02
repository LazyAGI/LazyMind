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
