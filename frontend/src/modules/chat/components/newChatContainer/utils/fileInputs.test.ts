import { describe, expect, it } from 'vitest';

import { getFileUrls } from './fileInputs';

describe('getFileUrls', () => {
  it('keeps the original filename for the persisted chat input', () => {
    const files = [{ uid: 'file-1', name: 'brief.pdf', uri: '/uploads/stored-file' }] as any;

    expect(getFileUrls(files)).toEqual([
      { uri: '/uploads/stored-file', base64: '', name: 'brief.pdf' },
    ]);
  });
});
