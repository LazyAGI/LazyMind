import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PaperPaneDivider } from './PaperPaneDivider';

afterEach(()=>vi.unstubAllGlobals());

describe('paper assistant divider', () => {
  it('lets keyboard users resize and reset the assistant within its limits', () => {
    const change = vi.fn();
    const view = render(<PaperPaneDivider width={400} maxWidth={600} onChange={change} label="Resize assistant" />);
    const divider = screen.getByRole('separator', {name: 'Resize assistant'});
    fireEvent.keyDown(divider, {key:'ArrowLeft'});
    expect(change).toHaveBeenLastCalledWith(424);
    view.rerender(<PaperPaneDivider width={590} maxWidth={600} onChange={change} label="Resize assistant" />);
    fireEvent.keyDown(divider, {key:'ArrowLeft'});
    expect(change).toHaveBeenLastCalledWith(600);
    fireEvent.keyDown(divider, {key:'Home'});
    expect(change).toHaveBeenLastCalledWith(320);
    fireEvent.doubleClick(divider);
    expect(change).toHaveBeenLastCalledWith(400);
  });
});

it('resizes a left-docked panel in the direction of the arrow',()=>{
 const change=vi.fn();render(<PaperPaneDivider width={400} maxWidth={600} side="left" onChange={change} label="Resize left panel"/>);
 fireEvent.keyDown(screen.getByRole('separator'),{key:'ArrowRight'});expect(change).toHaveBeenLastCalledWith(424);
});

it('previews pointer resizing once per frame and commits only the final width',()=>{
  vi.stubGlobal('PointerEvent',MouseEvent);
  const frames=new Map<number,FrameRequestCallback>();let nextFrame=0;
  vi.stubGlobal('requestAnimationFrame',(callback:FrameRequestCallback)=>{frames.set(++nextFrame,callback);return nextFrame;});
  vi.stubGlobal('cancelAnimationFrame',(id:number)=>frames.delete(id));
  const change=vi.fn(), preview=vi.fn();
  const view=render(<PaperPaneDivider width={400} maxWidth={800} onChange={change} onPreview={preview} label="Resize"/>);
  const divider=screen.getByRole('separator');
  divider.setPointerCapture=vi.fn();
  fireEvent.pointerDown(divider,{button:0,clientX:500});
  fireEvent.pointerMove(divider,{clientX:480});
  fireEvent.pointerMove(divider,{clientX:450});
  expect(change).not.toHaveBeenCalled();
  expect(frames.size).toBe(1);
  for(const callback of frames.values())callback(0);frames.clear();
  expect(preview).toHaveBeenLastCalledWith(450);
  fireEvent.pointerMove(divider,{clientX:440});
  fireEvent.pointerUp(divider,{clientX:430});
  expect(change).toHaveBeenCalledTimes(1);
  expect(change).toHaveBeenLastCalledWith(470);
  expect(preview).toHaveBeenLastCalledWith(undefined);
  expect(frames.size).toBe(0);
  fireEvent.lostPointerCapture(divider);
  expect(change).toHaveBeenCalledTimes(1);
  view.unmount();vi.unstubAllGlobals();
});

it('discards a cancelled drag and cleans up queued previews on unmount',()=>{
  vi.stubGlobal('PointerEvent',MouseEvent);
  const cancel=vi.fn();
  vi.stubGlobal('requestAnimationFrame',()=>12);
  vi.stubGlobal('cancelAnimationFrame',cancel);
  const change=vi.fn(),preview=vi.fn();
  const view=render(<PaperPaneDivider width={400} maxWidth={800} onChange={change} onPreview={preview} label="Resize"/>);
  const divider=screen.getByRole('separator');divider.setPointerCapture=vi.fn();
  fireEvent.pointerDown(divider,{button:0,clientX:500});
  fireEvent.pointerMove(divider,{clientX:450});
  fireEvent.pointerCancel(divider);
  expect(change).not.toHaveBeenCalled();
  expect(preview).toHaveBeenLastCalledWith(undefined);
  fireEvent.pointerDown(divider,{button:0,clientX:500});
  fireEvent.pointerMove(divider,{clientX:450});
  view.unmount();
  expect(cancel).toHaveBeenCalledWith(12);
  expect(change).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
});
