import { describe, expect, it } from "vitest";
import { isPaperLibrary } from "./store";

describe("persisted scene resolution", () => {
 it("recognizes only the server paper scene", () => {
  expect(isPaperLibrary("academic_papers")).toBe(true);
  expect(isPaperLibrary("general")).toBe(false);
  expect(isPaperLibrary("general")).toBe(false);
  expect(isPaperLibrary()).toBe(false);
 });
});

import { vi } from 'vitest';
import { paperFixture, libraryFixture } from './__tests__/fixtures';
import { confirmLibraryPdf, patchLibraryPaper } from './store';
const request = vi.hoisted(() => vi.fn());
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: { request } }));

describe('restored bibliography write contract', () => {
 it.each(['edit', 'pdf'] as const)('sends supported metadata when performing %s on a restored paper', async operation => {
  const paper = paperFixture({ metadata: { confidence: { title: 0.9 }, identifiers: { doi: '10.1234/a', pmid: '77', isbn: 'book' } } });
  request.mockResolvedValue({ data: { data: libraryFixture({ papers: [paper] }) } });
  if (operation === 'edit') await patchLibraryPaper('library', paper, { metadata: paper.metadata });
  else await confirmLibraryPdf('library', paper, 'document', 'request');
  const payload = request.mock.lastCall![0].data;
  const metadata = operation === 'edit' ? payload.metadata : payload.items[0].paper.metadata;
  expect(metadata).not.toHaveProperty('confidence');
  expect(metadata.identifiers).toEqual({ doi: '10.1234/a' });
  expect(paper.metadata.confidence).toEqual({ title: 0.9 });
  expect(paper.metadata.identifiers.pmid).toBe('77');
 });
});

it('updates note content through the scoped PATCH endpoint without sending its anchor', async()=>{
 const {updateLibraryNote}=await import('./store');
 const data=libraryFixture({papers:[paperFixture()]});request.mockResolvedValue({data:{data}});
 expect(await updateLibraryNote('library','paper/1','note/1','Revised')).toEqual({data});
 expect(request).toHaveBeenLastCalledWith(expect.objectContaining({method:'patch',url:'/api/core/academic/library/papers/paper%2F1/notes/note%2F1',params:{target_dataset_id:'library'},data:{content:'Revised'}}));
});
