import assert from 'node:assert/strict';
import test from 'node:test';

import {chunkText} from '../src/capture.js';

test('paginates a complete page without permanently truncating content', () => {
  const source = 'abcdefghijklmnop';
  const first = chunkText(source, 0, 6);
  const second = chunkText(source, first.nextOffset, 6);
  const third = chunkText(source, second.nextOffset, 6);

  assert.equal(first.complete, false);
  assert.equal(third.complete, true);
  assert.equal(third.nextOffset, null);
  assert.equal(first.text + second.text + third.text, source);
});

test('does not split a unicode surrogate pair between chunks', () => {
  const source = 'ab🐶cd';
  const first = chunkText(source, 0, 3);
  const second = chunkText(source, first.nextOffset, 3);
  const third = chunkText(source, second.nextOffset, 3);

  assert.equal(first.text + second.text + third.text, source);
  assert.equal(first.text.includes('\uFFFD'), false);
  assert.equal(second.text.includes('\uFFFD'), false);
  assert.equal(third.text.includes('\uFFFD'), false);
});
