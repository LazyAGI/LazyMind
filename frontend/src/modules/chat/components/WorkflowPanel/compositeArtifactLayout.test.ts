import { describe, expect, it } from 'vitest';

import {
  filterPresentCompositeItems,
  findAlignedCompositeRevision,
} from './compositeArtifactLayout';

describe('composite artifact layout', () => {
  it('uses exact page alignment before the single-input fallback', () => {
    const revisions = [{ sort_order: 1, value: 'one' }, { sort_order: 2, value: 'two' }];

    expect(findAlignedCompositeRevision(revisions, 2, true)?.value).toBe('two');
    expect(findAlignedCompositeRevision(revisions, 3, true)).toBeUndefined();
  });

  it('repeats one input across output pages when declared by the workflow', () => {
    const revisions = [{ sort_order: 1, value: 'reference' }];

    expect(findAlignedCompositeRevision(revisions, 8, true)?.value).toBe('reference');
    expect(findAlignedCompositeRevision(revisions, 8, false)).toBeUndefined();
  });

  it('removes absent cells instead of rendering placeholders', () => {
    expect(filterPresentCompositeItems(['input', 'missing', 'video'], (item) => item !== 'missing', true))
      .toEqual(['input', 'video']);
  });
});
