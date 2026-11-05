import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { PaperToolDock } from './PaperToolDock';
const tools = ['info', 'notes', 'translation', 'chat'].map(id => ({id, label:id, icon:<span>{id}</span>}));
describe('paper tool dock', () => {
  it.each(tools.map(tool=>tool.id))('closes the active %s panel on repeated click', activeTool => {
    const onChange=vi.fn();
    render(<PaperToolDock tools={tools} activeTool={activeTool} onChange={onChange} label="Tools"/>);
    fireEvent.click(screen.getByRole('button',{name:activeTool}));
    expect(onChange).toHaveBeenCalledWith(undefined);
  });
  it('opens an inactive panel directly', () => {
    const onChange=vi.fn();
    render(<PaperToolDock tools={tools} activeTool="notes" onChange={onChange} label="Tools"/>);
    fireEvent.click(screen.getByRole('button',{name:'translation'}));
    expect(onChange).toHaveBeenCalledWith('translation');
  });
});
