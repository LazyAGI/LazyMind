import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import FeaturedCapabilityMenu from './FeaturedCapabilityMenu';
import { useFeaturedSelectionStore } from '@/modules/chat/store/featuredSelection';
import { MemoryRouter, useLocation } from 'react-router-dom';

const list = vi.hoisted(() => vi.fn());
vi.mock('@/modules/showcase/api', () => ({ listShowcaseCases: list }));
vi.mock('@/modules/memory/skillApi', () => ({ enableBuiltinSkill: vi.fn() }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

function LocationProbe() {
  const location = useLocation();
  return <output>{location.pathname}{location.search}</output>;
}

describe('featured capability two-level menu', () => {
  beforeEach(() => { useFeaturedSelectionStore.setState({ selections: {} }); });
  it('keeps one entry and selects a function inside the same menu', async () => {
    const onChange = vi.fn();
    const onOpenChange = vi.fn();
    const controls = {
      skill: { value: 'case-a', selectedLabel: 'Writer', options: [{ value: 'case-a', label: 'Writer' }], ariaLabel: 'capability', heading: 'capabilities', disabled: false, onChange: vi.fn() },
      task: { options: [{ value: 'polish', label: 'Polish document' }], ariaLabel: 'function', heading: 'functions', disabled: false, onChange },
    };
    render(<FeaturedCapabilityMenu selectionKey='new' controls={controls} open onOpenChange={onOpenChange} disabled={false}
      runInBackground onBindingChange={vi.fn()} onPromptChange={vi.fn()} />);
    expect(screen.getAllByRole('button', { name: 'chat.featuredCapabilities' })).toHaveLength(1);
    fireEvent.click(screen.getByRole('option', { name: 'Writer' }));
    fireEvent.click(await screen.findByRole('option', { name: 'Polish document' }));
    expect(onChange).toHaveBeenCalledWith('polish');
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(useFeaturedSelectionStore.getState().selections.new).toEqual({ caseId: 'case-a', taskId: 'polish' });
  });

  it('restores a conversation selection and binds its workflow before sending', async () => {
    list.mockResolvedValue({ cases: [{ id: 'ppt', title: 'PPT', type: 'workflow', featured: true, workflow_ref: 'builtin:ppt', tasks: [] }] });
    useFeaturedSelectionStore.getState().setSelection('conversation-1', { caseId: 'ppt' });
    const binding = vi.fn();
    render(<FeaturedCapabilityMenu selectionKey='conversation-1' open={false} onOpenChange={vi.fn()} disabled={false}
      runInBackground onBindingChange={binding} onPromptChange={vi.fn()} />);
    await waitFor(() => expect(binding).toHaveBeenLastCalledWith([expect.objectContaining({ resource_id: 'builtin:ppt' })], false));
    expect(screen.getByRole('button', { name: 'chat.featuredCapabilities' })).toHaveAttribute('title', 'PPT');
  });

  it('never restores the old binding after clearing a selected capability', async () => {
    list.mockResolvedValue({ cases: [{ id: 'ppt', title: 'PPT', type: 'workflow', featured: true, workflow_ref: 'builtin:ppt', tasks: [] }] });
    useFeaturedSelectionStore.getState().setSelection('clear-case', { caseId: 'ppt' });
    const binding = vi.fn();
    render(<FeaturedCapabilityMenu selectionKey='clear-case' open onOpenChange={vi.fn()} disabled={false}
      runInBackground onBindingChange={binding} onPromptChange={vi.fn()} />);
    await waitFor(() => expect(binding).toHaveBeenLastCalledWith([expect.objectContaining({ resource_id: 'builtin:ppt' })], false));
    const before = binding.mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'showcase.clearCase' }));
    expect(binding.mock.calls.slice(before).every(([mentions]) => mentions.length === 0)).toBe(true);
  });

  it.each([false, true])('opens paper reading directly from the conversation selector, work=%s', async (runInBackground) => {
    list.mockResolvedValue({ cases: [{ id: 'paper-reading', title: '论文阅读', type: 'page', page_action: 'open_paper_library', featured: true, featured_order: 1, tasks: [] }] });
    const onOpenChange = vi.fn();
    const onPromptChange = vi.fn();
    render(<MemoryRouter><FeaturedCapabilityMenu selectionKey='reading' open onOpenChange={onOpenChange} disabled={false}
      runInBackground={runInBackground} onBindingChange={vi.fn()} onPromptChange={onPromptChange} /><LocationProbe /></MemoryRouter>);
    fireEvent.click(await screen.findByRole('option', { name: '论文阅读' }));
    expect(screen.getByText(`/lib/knowledge/list?scene=academic_papers&showcase_entry=${runInBackground ? 'work' : 'chat'}`)).toBeInTheDocument();
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(useFeaturedSelectionStore.getState().selections.reading).toBeUndefined();
    expect(onPromptChange).not.toHaveBeenCalled();
  });

  it('opens the controlled page option without changing the selected capability', () => {
    const onChange = vi.fn();
    const onPromptChange = vi.fn();
    render(<MemoryRouter><FeaturedCapabilityMenu selectionKey='new' open onOpenChange={vi.fn()} disabled={false}
      controls={{ skill: { value: 'writer', options: [{ value: 'paper-reading', label: '论文阅读', pageAction: 'open_paper_library' }], ariaLabel: 'capability', heading: 'capabilities', disabled: false, onChange } }}
      runInBackground onBindingChange={vi.fn()} onPromptChange={onPromptChange} /><LocationProbe /></MemoryRouter>);
    fireEvent.click(screen.getByRole('option', { name: '论文阅读' }));
    expect(screen.getByText('/lib/knowledge/list?scene=academic_papers&showcase_entry=work')).toBeInTheDocument();
    expect(useFeaturedSelectionStore.getState().selections.new).toEqual({ caseId: 'writer' });
    expect(onChange).not.toHaveBeenCalled();
    expect(onPromptChange).not.toHaveBeenCalled();
  });
});
