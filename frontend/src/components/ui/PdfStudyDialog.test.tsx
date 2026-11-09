import {fireEvent,render,screen} from '@testing-library/react';
import {expect,it,vi} from 'vitest';
import {PdfStudyDialog} from './PdfStudyDialog';

it('keeps study content in a portal over the PDF and closes with Escape or outside click',()=>{
 const close=vi.fn();
 const view=render(<main data-testid="reader"><PdfStudyDialog onClose={close}><button>符号</button></PdfStudyDialog></main>);
 const dialog=screen.getByRole('dialog',{name:'原文解读'});
 expect(view.container.contains(dialog)).toBe(false);
 expect(dialog).toContainElement(screen.getByRole('button',{name:'符号'}));
 expect(screen.getByRole('button',{name:'钉住研读窗口'})).toBeInTheDocument();
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
 const pin=screen.getByRole('button',{name:'钉住研读窗口'});const more=screen.getByText('More');
 more.focus();fireEvent.keyDown(more,{key:'Tab'});expect(pin).toHaveFocus();
 fireEvent.keyDown(pin,{key:'Tab',shiftKey:true});expect(more).toHaveFocus();
 dialog.focus();fireEvent.keyDown(dialog,{key:'Tab',shiftKey:true});expect(more).toHaveFocus();
 view.unmount();expect(view.container).not.toHaveAttribute('inert');expect(opener).toHaveFocus();opener.remove();
});

it('pins the study as a movable, resizable nonmodal window and restores the modal view',()=>{
 const close=vi.fn(),readPdf=vi.fn();
 const view=render(<main data-testid="reader"><button onClick={readPdf}>阅读 PDF</button><PdfStudyDialog onClose={close}><aside><header><h2>公式研读</h2></header><button>公式符号</button></aside></PdfStudyDialog></main>);
 const dialog=screen.getByRole('dialog',{name:'原文解读'});
 expect(view.container).toHaveAttribute('inert');
 fireEvent.click(screen.getByRole('button',{name:'钉住研读窗口'}));
 expect(dialog).not.toHaveAttribute('aria-modal');
 expect(dialog.parentElement).toHaveClass('is-pinned');
 expect(view.container).not.toHaveAttribute('inert');
 expect(screen.getByRole('button',{name:'取消钉住研读窗口'})).toHaveAttribute('aria-pressed','true');
 fireEvent.pointerDown(dialog.parentElement!);
 expect(close).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole('button',{name:'阅读 PDF'}));
 expect(readPdf).toHaveBeenCalledOnce();
 const left=parseFloat(dialog.style.left);
 const top=parseFloat(dialog.style.top);
 fireEvent.pointerDown(screen.getByRole('heading',{name:'公式研读'}),{button:0,pointerId:1,clientX:100,clientY:100});
 fireEvent.pointerMove(dialog,{pointerId:1,clientX:55,clientY:125});
 fireEvent.pointerUp(dialog,{pointerId:1});
 expect(parseFloat(dialog.style.left)).toBe(left-45);
 expect(parseFloat(dialog.style.top)).toBe(top+25);
 expect(dialog).toHaveClass('is-pinned');
 fireEvent.click(screen.getByRole('button',{name:'取消钉住研读窗口'}));
 expect(dialog).toHaveAttribute('aria-modal','true');
 expect(view.container).toHaveAttribute('inert');
 view.unmount();
});

it('offers minimize only for a pinned study and reports its window geometry',()=>{
 const minimize=vi.fn();
 render(<PdfStudyDialog onClose={vi.fn()} onMinimize={minimize}><aside><header><h2>公式研读</h2></header></aside></PdfStudyDialog>);
 expect(screen.queryByRole('button',{name:'最小化研读窗口'})).not.toBeInTheDocument();
 fireEvent.click(screen.getByRole('button',{name:'钉住研读窗口'}));
 const dialog=screen.getByRole('dialog',{name:'原文解读'});
 fireEvent.click(screen.getByRole('button',{name:'最小化研读窗口'}));
 expect(minimize).toHaveBeenCalledWith(expect.objectContaining({x:expect.any(Number),y:expect.any(Number),width:expect.any(Number),height:expect.any(Number)}));
 expect(dialog).not.toHaveAttribute('aria-modal');
});

it('restores a minimized study directly as a pinned nonmodal window',()=>{
 render(<main data-testid="reader"><PdfStudyDialog onClose={vi.fn()} initialPinned initialGeometry={{x:180,y:110,width:520,height:420}}><aside><header><h2>表格研读</h2></header></aside></PdfStudyDialog></main>);
 const dialog=screen.getByRole('dialog',{name:'原文解读'});
 expect(dialog.parentElement).toHaveClass('is-pinned');
 expect(dialog).toHaveStyle({left:'180px',top:'110px',width:'520px',height:'420px'});
 expect(screen.getByTestId('reader')).not.toHaveAttribute('inert');
});

it('resizes a pinned study from its edge without moving the PDF',()=>{
 render(<PdfStudyDialog onClose={vi.fn()}><aside><header><h2>公式研读</h2></header></aside></PdfStudyDialog>);
 fireEvent.click(screen.getByRole('button',{name:'钉住研读窗口'}));
 const dialog=screen.getByRole('dialog',{name:'原文解读'});
 const oldLeft=parseFloat(dialog.style.left),oldWidth=parseFloat(dialog.style.width);
 const west=window.document.querySelector('.pdf-study-resize-handle.is-w')!;
 fireEvent.pointerDown(west,{button:0,pointerId:3,clientX:100,clientY:100});
 fireEvent.pointerMove(west,{pointerId:3,clientX:50,clientY:100});
 fireEvent.pointerUp(west,{pointerId:3});
 expect(parseFloat(dialog.style.left)).toBe(oldLeft-50);
 expect(parseFloat(dialog.style.width)).toBe(oldWidth+50);
});
