import { describe, expect, it } from 'vitest';

import { markdownUrlTransform } from './index';

describe('markdownUrlTransform', () => {
  it('allows self-contained raster images used by delivered Markdown', () => {
    const image = 'data:image/png;base64,aGVsbG8=';
    expect(markdownUrlTransform(image)).toBe(image);
  });

  it('keeps unsafe data and script URLs blocked', () => {
    expect(markdownUrlTransform('data:image/svg+xml;base64,PHN2Zz4=')).toBe('');
    expect(markdownUrlTransform('javascript:alert(1)')).toBe('');
  });
});
