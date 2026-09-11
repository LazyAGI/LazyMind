import assert from 'node:assert/strict';
import test from 'node:test';
import vm from 'node:vm';
import {scrollPageContainer} from '../src/controller.js';

function pane(width, height, contentHeight, left = 0) {
  return {
    scrollLeft: 0, scrollTop: 0, clientWidth: width, clientHeight: height,
    scrollWidth: width, scrollHeight: contentHeight,
    getBoundingClientRect: () => ({left, top: 0, right: left + width, bottom: height}),
    scrollBy({top}) { this.scrollTop = Math.max(0, Math.min(contentHeight - height, this.scrollTop + top)); },
  };
}

test('scrolls the main nested document pane and reports its boundary', () => {
  const root = pane(1200, 800, 800);
  const sidebar = pane(200, 800, 2400);
  const content = pane(1000, 800, 3000, 200);
  const context = {
    document: {scrollingElement: root, querySelectorAll: () => [sidebar, content]},
    innerWidth: 1200, innerHeight: 800,
    getComputedStyle: () => ({overflowY: 'auto', overflowX: 'auto', visibility: 'visible', display: 'block'}),
  };
  const run = () => vm.runInNewContext(`(${scrollPageContainer.toString()})(0, 1200)`, context);
  assert.equal(run().moved, true);
  assert.equal(content.scrollTop, 1200);
  assert.equal(root.scrollTop, 0);
  assert.equal(sidebar.scrollTop, 0);
  assert.equal(run().at_boundary, true);
  assert.equal(run().moved, false);
  assert.equal(sidebar.scrollTop, 0);
});

test('ordinary pages still scroll the document root', () => {
  const root = pane(1200, 800, 3000);
  const result = vm.runInNewContext(`(${scrollPageContainer.toString()})(0, 600)`, {
    document: {scrollingElement: root, querySelectorAll: () => []},
    innerWidth: 1200, innerHeight: 800,
    getComputedStyle: () => ({overflowY: 'visible', visibility: 'visible', display: 'block'}),
  });
  assert.equal(root.scrollTop, 600);
  assert.equal(result.target, 'document');
  assert.equal(result.moved, true);
});
