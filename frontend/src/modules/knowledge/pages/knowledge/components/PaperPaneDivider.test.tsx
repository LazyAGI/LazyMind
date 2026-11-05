import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { PaperPaneDivider } from './PaperPaneDivider';

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
