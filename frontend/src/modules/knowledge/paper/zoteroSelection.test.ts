import { describe, expect, it } from 'vitest';
import type { ZoteroPreview } from './zoteroImport';
import { filterZoteroPreview, listZoteroPapers, zoteroBrowserIndex, importedZoteroKeys } from './zoteroSelection';

const item = (key: string, itemType: string, data: Record<string, unknown> = {}) => ({ key, data: { key, itemType, ...data } });
const collection = (key: string, name: string, parentCollection: string | false = false) => ({ key, data: { key, name, parentCollection } });
const fixture = (): ZoteroPreview => ({
  source_id: 'users:42', warnings: ['Provider notice'],
  collections: [collection('ROOT', 'Research'), collection('MODELS', 'Models', 'ROOT'), collection('OTHER', 'Other'), collection('EMPTY', 'Empty', 'ROOT')],
  items: [
    item('PAPER1', 'journalArticle', { title: 'Understanding attention', creators: [{ creatorType: 'author', firstName: 'Ada', lastName: 'Lovelace' }, { creatorType: 'editor', name: 'An editor' }], date: '2024-05-12', collections: ['MODELS'], relations: { 'dc:relation': ['https://www.zotero.org/users/42/items/PAPER2', 'https://www.zotero.org/users/42/items/STANDALONE'], 'owl:sameAs': 'https://example.com/unselected' }, attachments: [{ key: 'SHADOW', itemType: 'attachment' }] }),
    item('PDF1', 'attachment', { parentItem: 'PAPER1', contentType: 'application/pdf', filename: 'attention.pdf' }),
    item('ANNOT1', 'annotation', { parentItem: 'PDF1', annotationText: 'Evidence' }),
    item('NOTE1', 'note', { parentItem: 'PAPER1', note: '<p>A note</p>' }),
    item('HTML1', 'attachment', { parentItem: 'PAPER1', contentType: 'text/html' }),
    item('HTMLANNOT', 'annotation', { parentItem: 'HTML1' }),
    item('PAPER2', 'book', { title: 'Unselected paper', collections: ['OTHER'] }),
    item('PDF2', 'attachment', { parentItem: 'PAPER2', contentType: 'application/pdf' }),
    item('NOTE2', 'note', { parentItem: 'PAPER2' }),
    item('STANDALONE', 'attachment', { filename: 'independent.pdf', collections: ['ROOT'] }),
    item('STANDALONEANNOT', 'annotation', { parentItem: 'STANDALONE' }),
    item('LONENOTE', 'note', { note: 'Not a paper' }),
    item('DELETED', 'journalArticle', { deleted: true }),
  ],
});
const dataOf = (value: unknown) => (value as { data: Record<string, unknown> }).data;
const keys = (values: unknown[]) => values.map(value => String((value as { key: string }).key));

describe('Zotero paper selection', () => {
  it('lists papers and standalone PDFs with searchable metadata and owned material counts', () => {
    expect(listZoteroPapers(fixture())).toEqual([
      { key: 'PAPER1', title: 'Understanding attention', authors: ['Ada Lovelace'], year: '2024', pdfCount: 1, unavailablePdfCount: 0, noteCount: 1, annotationCount: 1, collections: ['Research / Models'], searchText: 'understanding attention ada lovelace 2024' },
      { key: 'PAPER2', title: 'Unselected paper', authors: [], year: '', pdfCount: 1, unavailablePdfCount: 0, noteCount: 1, annotationCount: 0, collections: ['Other'], searchText: 'unselected paper' },
      { key: 'STANDALONE', title: 'independent.pdf', authors: [], year: '', pdfCount: 1, unavailablePdfCount: 0, noteCount: 0, annotationCount: 1, collections: ['Research'], searchText: 'independent.pdf' },
    ]);
  });

  it('counts explicitly unavailable child and standalone PDFs without dropping their retryable records', () => {
    const preview = fixture();
    dataOf(preview.items[1]).localFileAvailable = false;
    dataOf(preview.items[4]).localFileAvailable = false;
    dataOf(preview.items[9]).localFileAvailable = false;
    preview.items.push(item('AVAILABLE', 'attachment', { parentItem: 'PAPER1', filename: 'available.pdf', localFileAvailable: true }),
      item('UNKNOWN', 'attachment', { parentItem: 'PAPER1', filename: 'unknown.pdf' }));
    const papers = listZoteroPapers(preview);
    expect(papers.map(({ key, pdfCount, unavailablePdfCount }) => ({ key, pdfCount, unavailablePdfCount }))).toEqual([
      { key: 'PAPER1', pdfCount: 3, unavailablePdfCount: 1 },
      { key: 'PAPER2', pdfCount: 1, unavailablePdfCount: 0 },
      { key: 'STANDALONE', pdfCount: 1, unavailablePdfCount: 1 },
    ]);
    const selected = filterZoteroPreview(preview, ['PAPER1', 'STANDALONE']);
    expect(keys(selected.items)).toEqual(['PAPER1', 'PDF1', 'ANNOT1', 'NOTE1', 'STANDALONE', 'STANDALONEANNOT', 'AVAILABLE', 'UNKNOWN']);
    expect(dataOf(selected.items[1]).localFileAvailable).toBe(false);
    expect(dataOf(selected.items[4]).localFileAvailable).toBe(false);
  });

  it('counts embedded notes only on supported PDFs and retains only the selected attachment notes', () => {
    const preview = fixture();
    dataOf(preview.items[1]).note = '<p>PDF reading note</p>';
    dataOf(preview.items[4]).note = '<p>Ignored web snapshot note</p>';
    dataOf(preview.items[7]).note = '<p>Unselected PDF note</p>';
    dataOf(preview.items[9]).note = '<p>Standalone PDF note</p>';
    preview.items.push(item('EMPTYNOTE', 'attachment', { parentItem: 'PAPER1', filename: 'empty.pdf', note: '  \n ' }));
    expect(listZoteroPapers(preview).map(({ key, noteCount }) => ({ key, noteCount }))).toEqual([
      { key: 'PAPER1', noteCount: 2 }, { key: 'PAPER2', noteCount: 2 }, { key: 'STANDALONE', noteCount: 1 },
    ]);
    const selected = filterZoteroPreview(preview, ['PAPER1']);
    expect(selected.items.map(value => dataOf(value).note).filter(value => typeof value === 'string' && value.trim())).toEqual(['<p>PDF reading note</p>', '<p>A note</p>']);
  });

  it('keeps only chosen papers, their materials and minimal folder ancestors without mutating preview', () => {
    const preview = fixture();
    const original = JSON.stringify(preview);
    const selected = filterZoteroPreview(preview, new Set(['PAPER1']));
    expect(keys(selected.items)).toEqual(['PAPER1', 'PDF1', 'ANNOT1', 'NOTE1']);
    expect(keys(selected.collections)).toEqual(['ROOT', 'MODELS']);
    expect(dataOf(selected.items[0])).not.toHaveProperty('attachments');
    expect(dataOf(selected.items[0]).relations).toEqual({});
    expect(selected.source_id).toBe('users:42');
    expect(selected.warnings).toEqual(['Provider notice']);
    expect(JSON.stringify(preview)).toBe(original);
  });

  it('retains relations only to selected papers and removes stale collection child/member references', () => {
    const preview = fixture();
    Object.assign(dataOf(preview.collections[0]), { children: [{ key: 'EMPTY' }], items: ['PAPER1', 'PAPER2'] });
    const selected = filterZoteroPreview(preview, ['PAPER1', 'STANDALONE']);
    expect(keys(selected.items)).toEqual(['PAPER1', 'PDF1', 'ANNOT1', 'NOTE1', 'STANDALONE', 'STANDALONEANNOT']);
    expect(dataOf(selected.items[0]).relations).toEqual({ 'dc:relation': ['https://www.zotero.org/users/42/items/STANDALONE'] });
    expect(dataOf(selected.collections[0])).not.toHaveProperty('children');
    expect(dataOf(selected.collections[0])).not.toHaveProperty('items');
  });

  it('produces no records for an empty selection and rejects selecting children or unknown keys', () => {
    expect(filterZoteroPreview(fixture(), []).items).toEqual([]);
    expect(filterZoteroPreview(fixture(), []).collections).toEqual([]);
    expect(() => filterZoteroPreview(fixture(), ['PDF1'])).toThrow(/选择/);
    expect(() => filterZoteroPreview(fixture(), ['UNKNOWN'])).toThrow(/选择/);
  });

  it('accepts flat export records and resolves children listed before their paper', () => {
    const preview: ZoteroPreview = { source_id: 'users:42', warnings: [], collections: [], items: [
      { key: 'ANNOT', itemType: 'annotation', parentItem: 'PDF' },
      { key: 'NOTE', itemType: 'note', parentItem: 'PAPER' },
      { key: 'PDF', itemType: 'attachment', parentItem: 'PAPER', path: 'storage/PDF/document.pdf' },
      { key: 'PAPER', itemType: 'conferencePaper', title: 'Out of order', creators: [{ name: 'A research group' }] },
    ] };
    expect(listZoteroPapers(preview)).toEqual([{ key: 'PAPER', title: 'Out of order', authors: ['A research group'], year: '', pdfCount: 1, unavailablePdfCount: 0, noteCount: 1, annotationCount: 1, collections: [], searchText: 'out of order a research group' }]);
    expect(keys(filterZoteroPreview(preview, ['PAPER']).items)).toEqual(['ANNOT', 'NOTE', 'PDF', 'PAPER']);
  });

  it('does not create an empty folder from a child-only collection membership', () => {
    const preview = fixture();
    dataOf(preview.items[1]).collections = ['OTHER'];
    const selected = filterZoteroPreview(preview, ['PAPER1']);
    expect(keys(selected.collections)).toEqual(['ROOT', 'MODELS']);
    expect(dataOf(selected.items[1]).collections).toEqual([]);
  });

  it.each([
    ['missing paper parent', (preview: ZoteroPreview) => { preview.items.push(item('ORPHAN', 'note', { parentItem: 'MISSING' })); }],
    ['missing annotation parent', (preview: ZoteroPreview) => { preview.items.push(item('ORPHAN', 'annotation', { parentItem: 'MISSING' })); }],
    ['missing collection', (preview: ZoteroPreview) => { dataOf(preview.items[0]).collections = ['MISSING']; }],
    ['missing collection parent', (preview: ZoteroPreview) => { preview.collections.push(collection('ORPHAN', 'Broken', 'MISSING')); }],
    ['duplicate item', (preview: ZoteroPreview) => { preview.items.push(item('PAPER1', 'book')); }],
    ['duplicate collection', (preview: ZoteroPreview) => { preview.collections.push(collection('ROOT', 'Duplicate')); }],
    ['item cycle', (preview: ZoteroPreview) => { dataOf(preview.items[0]).parentItem = 'PDF1'; }],
    ['collection cycle', (preview: ZoteroPreview) => { dataOf(preview.collections[0]).parentCollection = 'MODELS'; }],
    ['wrong annotation parent type', (preview: ZoteroPreview) => { dataOf(preview.items[2]).parentItem = 'PAPER1'; }],
  ])('rejects %s before selection can hide a malformed graph', (_label, mutate) => {
    const preview = fixture(); mutate(preview);
    expect(() => listZoteroPapers(preview)).toThrow(/Zotero/);
    expect(() => filterZoteroPreview(preview, ['PAPER2'])).toThrow(/Zotero/);
  });

  it('handles a 50000 item graph and shared collection ancestry without recursion or repeated subtree expansion', () => {
    const preview: ZoteroPreview = { source_id: 'users:42', warnings: [], collections: [collection('ROOT', 'Root')], items: [] };
    for (let index = 0; index < 10000; index++) {
      preview.items.push(item(`P${index}`, 'journalArticle', { title: `Paper ${index}`, collections: ['ROOT'] }),
        item(`A${index}`, 'attachment', { parentItem: `P${index}`, filename: 'paper.pdf' }),
        item(`N${index}`, 'note', { parentItem: `P${index}` }),
        item(`X${index}`, 'annotation', { parentItem: `A${index}` }),
        item(`Y${index}`, 'annotation', { parentItem: `A${index}` }));
    }
    const papers = listZoteroPapers(preview);
    expect(papers).toHaveLength(10000);
    expect(papers[9999]).toMatchObject({ key: 'P9999', pdfCount: 1, noteCount: 1, annotationCount: 2 });
    const selected = filterZoteroPreview(preview, ['P9999']);
    expect(keys(selected.items)).toEqual(['P9999', 'A9999', 'N9999', 'X9999', 'Y9999']);
    expect(keys(selected.collections)).toEqual(['ROOT']);
    preview.items.push(item('OVER', 'book'));
    expect(() => listZoteroPapers(preview)).toThrow(/上限/);
  }, 3000);
});


it('indexes ancestor folders without duplicating papers and preserves empty folders', () => {
  const index = zoteroBrowserIndex(fixture());
  expect(index.memberships.get('PAPER1')).toEqual(new Set(['MODELS', 'ROOT']));
  expect(index.memberships.get('PAPER2')).toEqual(new Set(['OTHER']));
  expect(index.folders.find(folder => folder.key === 'ROOT')?.children.map(folder => folder.key)).toEqual(['MODELS', 'EMPTY']);
  expect(index.folders.find(folder => folder.key === 'ROOT')?.children.find(folder => folder.key === 'EMPTY')?.count).toBe(0);
});

it('restores only mappings persisted for this source and this library', () => {
  const papers = listZoteroPapers(fixture());
  const persisted = [{zoteroSources:['users:42:PAPER1', 'users:99:PAPER2']}];
  expect([...importedZoteroKeys('users:42', papers, persisted)]).toEqual(['PAPER1']);
  expect([...importedZoteroKeys('users:99', papers, persisted)]).toEqual(['PAPER2']);
  expect(importedZoteroKeys('users:42', papers, []).size).toBe(0);
});
