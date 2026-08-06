import { describe, expect, it, vi } from 'vitest';

import {
  findSourceByCitationId,
  getSourceDedupKey,
  getSourceHref,
  getSourceLabel,
  getSourceSubtitle,
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

  it('adapts external labels, domains, deduplication, and opening', () => {
    const open = vi.fn();
    vi.stubGlobal('window', { open });

    expect(getSourceLabel(external)).toBe('Example article');
    expect(getSourceSubtitle(external)).toBe('example.com');
    expect(getSourceDedupKey(external)).toBe('external:https://example.com/article');
    expect(getSourceHref(external)).toBe(external.url);
    expect(openSource(external)).toBe(true);
    expect(open).toHaveBeenCalledWith(external.url, '_blank', 'noopener,noreferrer');

    vi.unstubAllGlobals();
  });

  it('keeps knowledge-base navigation and document-level deduplication', () => {
    expect(getSourceLabel(knowledge)).toBe('guide.pdf');
    expect(getSourceDedupKey(knowledge)).toBe('knowledge_base:kb-1:doc-1');
    expect(getSourceHref(knowledge)).toContain('/lib/knowledge/knowledge/kb-1/doc-1?');
    expect(getSourceHref(knowledge)).toContain('segement_id=segment-1');
  });

  it('finds sources using the internal citation locator without displaying it', () => {
    expect(findSourceByCitationId([external, knowledge], '3.1')).toBe(external);
    expect(getSourceLabel(findSourceByCitationId([external], '3.1'))).not.toContain('3.1');
  });

  it('does not open unsafe external schemes', () => {
    expect(getSourceHref({ source_type: 'external', url: 'javascript:alert(1)' })).toBe('');
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

  it('normalizes complete source markers without keeping the markdown title', () => {
    expect(
      normalizeSourceMarkers('[1](#source-3.1 "Assistants migration guide | OpenAI API")'),
    ).toBe('[1](#source-3.1)');
  });

  it('repairs only an incomplete source marker at the streaming boundary', () => {
    expect(normalizeSourceMarkers('[1](#source-3.1 "Assistants migration guide')).toBe(
      '[1](#source-3.1)',
    );
    expect(normalizeSourceMarkers('[1](#source-3.1 "Assistants\n正文')).toBe(
      '[1](#source-3.1 "Assistants\n正文',
    );
    expect(normalizeSourceMarkers('[guide](https://example.com "Example')).toBe(
      '[guide](https://example.com "Example',
    );
  });

  it('collapses only the same adjacent source marker repeated in parentheses', () => {
    const title = 'NeurIPS-2024-hipporag.pdf';
    const source = `[2](#source-2.3 "${title}")`;

    expect(normalizeSourceMarkers(`${source}(${source})`)).toBe('[2](#source-2.3)');
    expect(normalizeSourceMarkers(`${source}（${source}）`)).toBe('[2](#source-2.3)');
    expect(
      normalizeSourceMarkers(`${source}([1](#source-1.5 "Another.pdf"))`),
    ).toBe('[2](#source-2.3)([1](#source-1.5))');
    expect(normalizeSourceMarkers(`${source}（${title}）`)).toBe(
      `[2](#source-2.3)（${title}）`,
    );
  });
});
