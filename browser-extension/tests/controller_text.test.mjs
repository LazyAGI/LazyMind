import assert from 'node:assert/strict';
import test from 'node:test';

import {
  intersectionPoint,
  normalizeText,
  normalizeViewportPoint,
  rectFromQuads,
} from '../src/controller.js';

test('drops zero-width-only accessibility labels', () => {
  assert.equal(normalizeText('\u200b'), '');
  assert.equal(normalizeText(' \u200b\u2060\ufeff '), '');
});

test('keeps meaningful text while removing safe invisible separators', () => {
  assert.equal(normalizeText(' Lazy\u200bMind  Browser '), 'LazyMind Browser');
  assert.equal(normalizeText('中文\u200d内容'), '中文\u200d内容');
});

test('accepts an intersection point inside the CSS viewport', () => {
  assert.deepEqual(normalizeViewportPoint(120.5, 240, {width: 800, height: 600}), {
    x: 120.5,
    y: 240,
  });
});

test('rejects an intersection point outside the CSS viewport', () => {
  assert.throws(
    () => normalizeViewportPoint(800, 100, {width: 800, height: 600}),
    (error) => error.code === 'COORDINATE_OUT_OF_BOUNDS',
  );
});

test('derives a clickable rectangle from a text-node content quad', () => {
  assert.deepEqual(
    rectFromQuads([[100, 40, 260, 40, 260, 64, 100, 64]]),
    {x: 100, y: 40, width: 160, height: 24},
  );
  assert.equal(rectFromQuads([[0, 0, 0, 0, 0, 0, 0, 0]]), null);
});

test('computes a grid intersection from the column x and row y', () => {
  assert.deepEqual(
    intersectionPoint(
      {x: 100, y: 520, width: 90, height: 30},
      {x: 700, y: 60, width: 80, height: 30},
      {width: 1200, height: 800},
    ),
    {x: 740, y: 535},
  );
});

test('rejects an intersection outside the current viewport', () => {
  assert.throws(
    () => intersectionPoint(
      {x: 100, y: 900, width: 90, height: 30},
      {x: 700, y: 60, width: 80, height: 30},
      {width: 1200, height: 800},
    ),
    (error) => error.code === 'COORDINATE_OUT_OF_BOUNDS',
  );
});
