import {fireEvent,render,screen} from '@testing-library/react';
import {afterEach,beforeEach,expect,it,vi} from 'vitest';
import {PdfPinDock,type DockItem} from './PdfPinDock';

const items:DockItem[]=[
  {id:'crop-1',title:'第 1 页的截取区域',kind:'region'},
  {id:'formula-2',title:'公式研读 · Attention',kind:'formula'},
];

beforeEach(()=>window.localStorage.clear());
afterEach(()=>vi.restoreAllMocks());

it('shows a draggable circular hub and six accessible restore slots',()=>{
  const restore=vi.fn();
  render(<PdfPinDock items={items} onRestore={restore} storageKey="paper-a"/>);
  expect(screen.getByRole('button',{name:'展开已收起内容（2）'})).toBeInTheDocument();
  expect(screen.queryByRole('button',{name:/恢复公式研读/})).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button',{name:'展开已收起内容（2）'}));
  const slots=document.querySelectorAll('[data-dock-slot]');
  expect(slots).toHaveLength(6);
  expect(slots[0]).not.toHaveAttribute('disabled');
  expect(slots[1]).not.toHaveAttribute('disabled');
  expect(slots[2]).toHaveAttribute('disabled');
  fireEvent.click(screen.getByRole('button',{name:'恢复公式研读 · Attention'}));
  expect(restore).toHaveBeenCalledWith('formula-2');
  expect(screen.queryByRole('button',{name:'恢复公式研读 · Attention'})).not.toBeInTheDocument();
});

it('drags without opening, clamps the hub, and persists its position per paper',()=>{
  const restore=vi.fn();
  const view=render(<PdfPinDock items={items} onRestore={restore} storageKey="paper-a"/>);
  const hub=screen.getByRole('button',{name:'展开已收起内容（2）'});
  const dock=hub.closest<HTMLElement>('.pdf-pin-dock')!;
  const initialLeft=parseFloat(dock.style.left);
  const initialTop=parseFloat(dock.style.top);
  fireEvent.pointerDown(hub,{button:0,pointerId:1,clientX:initialLeft+25,clientY:initialTop+25});
  fireEvent.pointerMove(hub,{pointerId:1,clientX:initialLeft-95,clientY:initialTop-55});
  fireEvent.pointerUp(hub,{pointerId:1});
  fireEvent.click(hub);
  expect(parseFloat(dock.style.left)).toBe(initialLeft-120);
  expect(parseFloat(dock.style.top)).toBe(initialTop-80);
  expect(screen.queryByRole('button',{name:'恢复公式研读 · Attention'})).not.toBeInTheDocument();
  view.unmount();
  const saved=render(<PdfPinDock items={items} onRestore={restore} storageKey="paper-a"/>);
  expect(parseFloat(screen.getByRole('button',{name:'展开已收起内容（2）'}).closest<HTMLElement>('.pdf-pin-dock')!.style.left)).toBe(initialLeft-120);
  saved.unmount();
  render(<PdfPinDock items={items} onRestore={restore} storageKey="paper-b"/>);
  expect(parseFloat(screen.getByRole('button',{name:'展开已收起内容（2）'}).closest<HTMLElement>('.pdf-pin-dock')!.style.left)).toBe(initialLeft);
});

it('supports English labels and Escape, and renders nothing when there are no minimized items',()=>{
  const restore=vi.fn();
  const view=render(<PdfPinDock items={items} onRestore={restore} language="en"/>);
  const hub=screen.getByRole('button',{name:'Show minimized pins (2)'});
  fireEvent.click(hub);
  expect(screen.getByRole('button',{name:'Restore 公式研读 · Attention'})).toBeInTheDocument();
  fireEvent.keyDown(hub.closest('.pdf-pin-dock')!,{key:'Escape'});
  expect(screen.queryByRole('button',{name:'Restore 公式研读 · Attention'})).not.toBeInTheDocument();
  view.rerender(<PdfPinDock items={[]} onRestore={restore} language="en"/>);
  expect(screen.queryByRole('button',{name:'Show minimized pins (0)'})).not.toBeInTheDocument();
});
