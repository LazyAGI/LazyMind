import { beforeEach, describe, expect, it } from 'vitest';
import { useFeaturedSelectionStore } from './featuredSelection';

describe('featured resume presentation persistence', () => {
  beforeEach(() => useFeaturedSelectionStore.setState({ selections: {} }));
  it('keeps presentation choices when the task changes and the conversation receives its real id', () => {
    const store = useFeaturedSelectionStore.getState();
    const resumePresentation = { template: 'editorial' as const, photoFit: 'cover' as const, photoUri: '/uploads/portrait.png' };
    store.setSelection('new', { caseId: 'tailored-resume-generator', resumePresentation });
    store.setSelection('new', { caseId: 'tailored-resume-generator', taskId: 'tailor-for-job' });
    store.moveSelection('new', 'conversation');
    expect(useFeaturedSelectionStore.getState().selections.new).toBeUndefined();
    expect(useFeaturedSelectionStore.getState().selections.conversation).toEqual({ caseId: 'tailored-resume-generator', taskId: 'tailor-for-job', resumePresentation });
  });
  it('does not carry resume settings into a different selected capability', () => {
    const store = useFeaturedSelectionStore.getState();
    store.setSelection('new', { caseId: 'tailored-resume-generator', resumePresentation: { template: 'sidebar', photoFit: 'contain' } });
    store.setSelection('new', { caseId: 'ppt' });
    expect(useFeaturedSelectionStore.getState().selections.new).toEqual({ caseId: 'ppt' });
  });
});
