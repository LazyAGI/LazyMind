import { describe, expect, it, vi } from 'vitest';

import {
  findSourceByCitationId,
  getCitationSources,
  getDisplaySources,
  getSearchSources,
  getSourceHref,
  moveSourceMarkersToParagraphEnd,
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

  it('gives every displayed source a click target', () => {
    const open = vi.fn();
    vi.stubGlobal('window', { open });

    expect(getSourceHref(external)).toBe(external.url);
    expect(openSource(external)).toBe(true);
    expect(open).toHaveBeenCalledWith(external.url, '_blank', 'noopener,noreferrer');
    expect(getSourceHref(knowledge)).toContain('/lib/knowledge/knowledge/kb-1/doc-1?');
    expect(getSourceHref(knowledge)).toContain('segement_id=segment-1');
    expect(getSourceHref({ source_type: 'external', url: 'not-yet-resolvable' })).toBe('not-yet-resolvable');
    expect(getSourceHref({ source_type: 'external', url: 'javascript:alert(1)', index: '5.1' })).toBe('#source-5.1');
    expect(getSourceHref({ url: 'https://legacy.example/page', index: '6.1' })).toBe('https://legacy.example/page');
    expect(getSourceHref({ ...knowledge, dataset_id: 'default' })).toContain('/default/doc-1?');
    expect(openSource({ file_name: 'temporary.txt', dataset_id: 'default' })).toBe(true);

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

  it('deduplicates unified roles, keeps every hit, and sorts cited before fetched before searched', () => {
    const searchedOnly = {
      source_type: 'external',
      title: 'Search result',
      url: 'https://search.example/result',
      source_roles: ['searched'],
    };
    const fetchedOnly = {
      source_type: 'external',
      title: 'Fetched page',
      url: 'https://fetched.example/page',
      source_roles: ['fetched'],
    };

    const sources = [
      searchedOnly,
      fetchedOnly,
      { ...external, source_roles: ['cited'] },
      { ...external, index: '3.2', source_roles: ['searched'] },
      { ...knowledge, source_roles: ['cited'] },
    ];
    expect(getDisplaySources(sources)).toEqual([
      searchedOnly,
      fetchedOnly,
      { ...external, source_roles: ['cited', 'searched'] },
      { ...knowledge, source_roles: ['cited'] },
    ]);
    expect(getSearchSources(sources)).toEqual([
      { ...external, source_roles: ['cited', 'searched'] },
      { ...knowledge, source_roles: ['cited'] },
      fetchedOnly,
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

  it('moves inline source markers to the end of each paragraph', () => {
    const first = '[1](#source-1.1)';
    const second = '[2](#source-2.1)';
    expect(moveSourceMarkersToParagraphEnd(
      `第一句${first}。第二句${second}。\n\n下一段${first}。`,
    )).toBe(`第一句。第二句。${first}${second}\n\n下一段。${first}`);
    expect(moveSourceMarkersToParagraphEnd(
      `\`\`\`\ncode ${first}\n\`\`\`\n\n正文${first}。继续。`,
    )).toBe(`\`\`\`\ncode ${first}\n\`\`\`\n\n正文。继续。${first}`);
    expect(moveSourceMarkersToParagraphEnd(
      `- A ${first}\n- B ${second}`,
    )).toBe(`- A${first} \n- B${second} `);
    const continued = `- A ${first}\n  continuation\n- B ${second}`;
    expect(moveSourceMarkersToParagraphEnd(continued)).toBe(continued);
  });

  it('leaves GFM table citations in their cells', () => {
    const first = '[1](#source-1.1)';
    const second = '[2](#source-2.1)';
    const table = [
      '| 模型 | 价格 |',
      '|---|---|',
      `| A ${first} | $1 |`,
      `| B ${second} | $2 |`,
    ].join('\n');
    expect(moveSourceMarkersToParagraphEnd(table)).toBe(table);
    expect(moveSourceMarkersToParagraphEnd(
      `${table}\n\n第一句${first}。第二句${second}。`,
    )).toBe(`${table}\n\n第一句。第二句。${first}${second}`);
  });
});
