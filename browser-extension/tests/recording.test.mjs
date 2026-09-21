import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from '../../frontend/node_modules/jsdom/lib/api.js';
import { BrowserRecorder, installPageRecording, readPageRecording } from '../src/recording.js';

function page() {
  const dom = new JSDOM('<h1>Report</h1><input id="query"><input id="api-key" type="password"><button id="export">Export</button>', { url: 'https://example.com/report?token=private' });
  const previous = {};
  const values = { document: dom.window.document, Element: dom.window.Element, MutationObserver: dom.window.MutationObserver, location: dom.window.location, CSS: { escape: (value) => value } };
  for (const [key, value] of Object.entries(values)) { previous[key] = globalThis[key]; globalThis[key] = value; }
  dom.window.Element.prototype.getClientRects = () => [{ width: 100, height: 20 }];
  const handlers = new Map();
  const add = document.addEventListener.bind(document);
  document.addEventListener = (type, fn, ...rest) => { handlers.set(type, fn); add(type, fn, ...rest); };
  return { handlers, close() { globalThis.__lazymindSkillRecording?.stop(); dom.window.close(); for (const key of Object.keys(values)) { if (previous[key] === undefined) delete globalThis[key]; else globalThis[key] = previous[key]; } } };
}

test('captures ordered actions and raw inputs including password fields, omits URL queries', () => {
  const fixture = page();
  try {
    installPageRecording('r', Date.now() - 100, Date.now() + 10000);
    const query = document.querySelector('#query'); query.value = 'quarterly report';
    fixture.handlers.get('input')({ isTrusted: true, target: query, inputType: 'insertText' });
    const secret = document.querySelector('#api-key'); secret.value = 'test-secret-value';
    fixture.handlers.get('input')({ isTrusted: true, target: secret, inputType: 'insertText' });
    fixture.handlers.get('keydown')({ isTrusted: true, target: secret, key: 'x' });
    fixture.handlers.get('click')({ isTrusted: true, target: document.querySelector('button'), clientX: 50, clientY: 20, button: 0 });
    const result = readPageRecording('r', true);
    assert.deepEqual(result.events.map((event) => event.kind), ['dom', 'input', 'input', 'keydown', 'click']);
    assert.equal(result.events[1].value, 'quarterly report');
    assert.equal(result.events[2].value, 'test-secret-value');
    assert.equal(result.events[3].key, 'x');
    assert.equal(result.events[4].target.selector, '#export');
    const text = JSON.stringify(result);
    assert.ok(text.includes('test-secret-value'));
    assert.equal(result.events[2].target.selector, '#api-key');
    assert.ok(!text.includes('token=private'));
    assert.equal(globalThis.__lazymindSkillRecording, undefined);
  } finally { fixture.close(); }
});

test('cancel removes listeners and expired or wrong sessions expose no events', () => {
  const fixture = page();
  try {
    installPageRecording('r', Date.now(), Date.now() + 10000);
    assert.deepEqual(readPageRecording('wrong', false), { events: [], gaps: true });
    readPageRecording('r', true);
    fixture.handlers.get('click')({ isTrusted: true, target: document.querySelector('button'), clientX: 1, clientY: 2, button: 0 });
    assert.deepEqual(readPageRecording('r', false), { events: [], gaps: true });
  } finally { fixture.close(); }
});

test('browser session is explicitly tab scoped, streams survive navigation, and cleanup runs', async () => {
  const calls = []; const listeners = new Set();
  const browser = {
    tabs: { query: async () => [{ id: 1, title: 'Page', url: 'https://example.com' }], get: async (id) => ({ id, url: 'https://example.com' }), onUpdated: { addListener: (fn) => listeners.add(fn), removeListener: (fn) => listeners.delete(fn) } },
    scripting: { executeScript: async (options) => { calls.push(options); return [{ result: options.func === installPageRecording ? { installed: true } : { events: [] } }]; } },
  };
  const recorder = new BrowserRecorder(browser);
  const result = await recorder.dispatch('recording_start', { tab_id: '1', started_at: Date.now() });
  await assert.rejects(() => recorder.dispatch('recording_start', { tab_id: '1', started_at: Date.now() }), /already running/);
  recorder.receive({ session_id: result.session_id, event: { kind: 'click', seconds: 1 } }, { tab: { id: 2 }, frameId: 0 });
  recorder.receive({ session_id: result.session_id, event: { kind: 'click', seconds: 2 } }, { tab: { id: 1 }, frameId: 0 });
  const events = await recorder.dispatch('recording_read', { session_id: result.session_id });
  assert.equal(events.events.length, 1); assert.equal(events.events[0].seconds, 2);
  assert.ok(calls.every((call) => call.target.tabId === 1 && call.target.allFrames === false));
  await recorder.dispatch('recording_cancel', { session_id: result.session_id });
  assert.equal(recorder.sessions.size, 0);
  await recorder.dispose(); assert.equal(listeners.size, 0);
});


test('preserves Unicode, pasted input and content that resembles secrets', () => {
  const fixture = page();
  try {
    installPageRecording('r', Date.now(), Date.now() + 10000);
    const query = document.querySelector('#query');
    query.value = '你好😀 email@example.com token=test-token';
    fixture.handlers.get('input')({ isTrusted: true, target: query, inputType: 'insertFromPaste' });
    fixture.handlers.get('keydown')({ isTrusted: true, target: query, key: '好' });
    const result = readPageRecording('r', true);
    assert.equal(result.events[1].value, query.value);
    assert.equal(result.events[1].input_type, 'insertFromPaste');
    assert.equal(result.events[2].key, '好');
  } finally { fixture.close(); }
});
