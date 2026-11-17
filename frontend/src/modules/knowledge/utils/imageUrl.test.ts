import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  resolveMarkdownImageSourceFromMap,
  resolveMarkdownImageUrlAsync,
} from './imageUrl';

const mediaUrls = {
  'docs/assets/diagram.png': '/static-files/session/diagram.png?expires=1&sig=test',
  'asset://asset-generated': '/static-files/session/generated.png?expires=1&sig=test',
};

describe('resolveMarkdownImageSourceFromMap', () => {
  it('maps an original Markdown reference to its authorized display URL', () => {
    expect(resolveMarkdownImageSourceFromMap(
      'docs/assets/diagram.png',
      mediaUrls,
    )).toBe('/static-files/session/diagram.png?expires=1&sig=test');
  });

  it('maps an asset reference by media asset id', () => {
    expect(resolveMarkdownImageSourceFromMap(
      'asset://asset-generated',
      mediaUrls,
    )).toBe('/static-files/session/generated.png?expires=1&sig=test');
  });

  it('does not guess by filename when the source reference is different', () => {
    expect(resolveMarkdownImageSourceFromMap(
      'other/diagram.png',
      mediaUrls,
    )).toBe('other/diagram.png');
  });

  it('maps encoded spaces and Unicode without changing the signed URL', () => {
    const source = '/Users/test/Library/Application Support/配图.png';
    const signed = '/static-files/image.png?expires=4102444800&sig=a%2Fb';
    expect(resolveMarkdownImageSourceFromMap(encodeURI(source), { [source]: signed }))
      .toBe(signed);
  });

  it('prefers an exact match over normalized alternatives', () => {
    expect(resolveMarkdownImageSourceFromMap('images/photo%20one.png', {
      'images/photo one.png': 'space.png',
      'images/photo%20one.png': 'exact.png',
    })).toBe('exact.png');
  });

  it('does not match an encoded slash to a different file path', () => {
    expect(resolveMarkdownImageSourceFromMap('images/a%2Fb.png', {
      'images/a/b.png': 'wrong.png',
    })).toBe('images/a%2Fb.png');
  });
});

describe('resolveMarkdownImageUrlAsync', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('uses an unexpired signed Core URL without signing it again', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);

    const resolved = await resolveMarkdownImageUrlAsync(
      '/static-files/subagent/user/task/image.jpg?expires=4102444800&sig=test',
    );

    expect(resolved).toContain(
      '/api/core/static-files/subagent/user/task/image.jpg?expires=4102444800&sig=test',
    );
    expect(fetch).not.toHaveBeenCalled();
  });
});
