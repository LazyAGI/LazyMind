import { describe, expect, it } from 'vitest';
import {
  appendPromptToDraft,
  canManagePrompt,
  setPromptFavorite,
} from '../../frontend/src/modules/chat/components/PromptModal/promptLibrary.ts';

describe('prompt library rules', () => {
  it('appends a phrase on a new line without discarding the draft', () => {
    expect(appendPromptToDraft('Existing draft  ', '  New phrase  ')).toBe(
      'Existing draft\nNew phrase',
    );
    expect(appendPromptToDraft('', 'New phrase')).toBe('New phrase');
    expect(appendPromptToDraft('Existing draft', '   ')).toBe('Existing draft');
  });

  it('allows management only for custom phrases', () => {
    expect(canManagePrompt({ source: 'custom' })).toBe(true);
    expect(canManagePrompt({ source: 'preset' })).toBe(false);
  });

  it('updates only the targeted favorite for optimistic update and rollback', () => {
    const prompts = [
      { id: 'one', is_favorite: false },
      { id: 'two', is_favorite: true },
    ];
    const updated = setPromptFavorite(prompts, 'one', true);
    expect(updated).toEqual([
      { id: 'one', is_favorite: true },
      { id: 'two', is_favorite: true },
    ]);
    expect(setPromptFavorite(updated, 'one', false)).toEqual(prompts);
  });
});
