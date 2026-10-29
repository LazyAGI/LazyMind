import { expect, it } from 'vitest';
import { libraryFixture, paperFixture } from './__tests__/fixtures';
import { indexFolderCounts, indexLibrary } from './libraryIndex';

it('indexes attachments by confirmed owner and ignores detached or unavailable files', () => {
  const data = libraryFixture();
  data.attachments[0].contentRef = "stored";
  data.attachments.push({ ...data.attachments[0], id: 'unconfirmed' });
  expect(indexLibrary(data).materials.get(data.papers[0].id)?.pdfs).toHaveLength(1);
  data.attachments[0].available = false;
  expect(indexLibrary(data).materials.get(data.papers[0].id)?.pdfs).toHaveLength(0);
});
it('deduplicates ancestor counts for papers in several nested collections', () => {
  const data = libraryFixture({ folders: [{ id:'root',name:'Root',parentId:null },{id:'a',name:'A',parentId:'root'},{id:'b',name:'B',parentId:'root'}], papers: [paperFixture({folderId:'a',collectionIds:['a','b','root']})] });
  expect(Object.fromEntries(indexFolderCounts(data).counts)).toEqual({root:1,a:1,b:1});
});
it('indexes ten thousand papers without repeatedly scanning the attachment array', () => {
  const base = libraryFixture();
  const papers = Array.from({length:10000},(_,i)=>paperFixture({id:`p${i}`,attachmentIds:[`a${i}`]}));
  const attachments = papers.map((paper,i)=>({...base.attachments[0],id:`a${i}`,paperId:paper.id}));
  let reads=0;
  const counted = new Proxy(attachments,{get(target,key,receiver){if (typeof key === 'string' && /^\d+$/.test(key)) reads++;return Reflect.get(target,key,receiver);}});
  const index=indexLibrary({...base,papers,attachments:counted});
  expect(index.materials.size).toBe(10000);expect(reads).toBeLessThanOrEqual(10000);
});
