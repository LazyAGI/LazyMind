import { describe, expect, it, vi } from 'vitest';

import {
  findSourceByCitationId,
  getCitationSources,
  getDisplaySources,
  getSearchSources,
  getSourceHref,
  normalizeSourceMarkers,
  openSource,
  stripRedundantSourceUrls,
} from '../../frontend/src/modules/chat/utils/sourceAdapter.ts';

describe('chat source adapter', () => {
  const external = {
    source_type: 'external',
    index: '3.1',
    title: 'Example article',
    url: 'https://example.com/article/#section',
    content: 'External evidence',
  };
  const knowledge = {
    index: '4.1',
    file_name: 'guide.pdf',
    dataset_id: 'kb-1',
    document_id: 'doc-1',
    segement_id: 'segment-1',
    group_name: 'block',
    segment_number: 2,
  };

  it('opens safe external and knowledge-base sources and rejects unsafe URLs', () => {
    const open = vi.fn();
    vi.stubGlobal('window', { open });

    expect(getSourceHref(external)).toBe(external.url);
    expect(openSource(external)).toBe(true);
    expect(open).toHaveBeenCalledWith(external.url, '_blank', 'noopener,noreferrer');
    expect(getSourceHref(knowledge)).toContain('/lib/knowledge/knowledge/kb-1/doc-1?');
    expect(getSourceHref(knowledge)).toContain('segement_id=segment-1');
    expect(getSourceHref({ source_type: 'external', url: 'javascript:alert(1)' })).toBe('');

    vi.unstubAllGlobals();
  });

  it('keeps source identities for citation lookup without drawer deduplication', () => {
    const duplicatePage = { ...external, index: '3.2', source_roles: ['cited'] };
    expect(findSourceByCitationId([external, knowledge], '3.1')).toBe(external);
    expect(getCitationSources([
      { ...external, source_roles: ['cited', 'searched'] },
      duplicatePage,
    ])).toEqual([
      { ...external, source_roles: ['cited', 'searched'] },
      duplicatePage,
    ]);
  });

  it('deduplicates unified roles, filters searched sources, and supports legacy maps', () => {
    const searchedOnly = {
      source_type: 'external',
      title: 'Search result',
      url: 'https://search.example/result',
      source_roles: ['searched'],
    };

    const sources = [
      { ...external, source_roles: ['cited'] },
      { ...external, index: '3.2', source_roles: ['searched'] },
      { ...knowledge, source_roles: ['cited'] },
      searchedOnly,
    ];
    expect(getDisplaySources(sources)).toEqual([
      { ...external, source_roles: ['cited', 'searched'] },
      { ...knowledge, source_roles: ['cited'] },
      searchedOnly,
    ]);
    expect(getSearchSources(sources)).toEqual([
      { ...external, source_roles: ['cited', 'searched'] },
      searchedOnly,
    ]);
    expect(getDisplaySources({ '3.1': { ...external, index: undefined } })).toEqual([
      { ...external, index: '3.1', source_roles: ['cited'] },
    ]);
    expect(getSearchSources({ '3.1': { ...external, index: undefined } })).toEqual([
      { ...external, index: '3.1', source_roles: ['cited'] },
    ]);
  });

  it('removes only a redundant URL immediately following a source marker', () => {
    const source = '[1](#source-3.1 "Concurrent Execution")';

    expect(stripRedundantSourceUrls(`${source}(https://docs.python.org/page)。这使得：`)).toBe(
      `${source}。这使得：`,
    );
    expect(stripRedundantSourceUrls(`${source}（https://docs.python.org/page）正文`)).toBe(
      `${source}正文`,
    );
    expect(stripRedundantSourceUrls('普通链接（https://example.com）保持不变')).toBe(
      '普通链接（https://example.com）保持不变',
    );
  });

  it('normalizes complete, streaming-boundary, and duplicate source markers', () => {
    expect(
      normalizeSourceMarkers('[1](#source-3.1 "Assistants migration guide | OpenAI API")'),
    ).toBe('[1](#source-3.1)');
    expect(normalizeSourceMarkers('[1](#source-3.1 "Assistants migration guide')).toBe(
      '[1](#source-3.1)',
    );
    const source = '[2](#source-2.3 "NeurIPS-2024-hipporag.pdf")';
    expect(normalizeSourceMarkers(`${source}(${source})`)).toBe('[2](#source-2.3)');
  });
});
