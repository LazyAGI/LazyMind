import assert from 'node:assert/strict';
import test from 'node:test';
import { collectCatchHardcodedMatches } from './check-error-prompts.mjs';

const check = (source) => collectCatchHardcodedMatches(source, 'fixture.tsx');

test('catches request failure copy, including braces inside a string', () => {
  for (const body of [
    `message.error(t('page.saveFailed'));`,
    `console.warn('}}}', error); message.warning('Request failed');`,
    `if (ready) { setRequestError(t('page.saveFailed')); }`,
  ]) {
    assert.equal(check(`try { await request(); } catch (error) { ${body} }`).length, 1);
  }
});

test('keeps successful partial-result notices outside a preceding catch', () => {
  const source = `
    try {
      for (const item of items) { try { await download(item); } catch {} }
      message.warning(t('page.partialFailed'));
    } catch (error) { message.error(getLocalizedErrorMessage(error)); }
  `;
  assert.deepEqual(check(source), []);
});

test('allows the shared catalog and existing client-only validation messages', () => {
  assert.deepEqual(check(`try {} catch (error) {
    message.error(getLocalizedErrorMessage(error));
    message.error(t('errors.2000509'));
    message.error(t('page.copyFailed'));
    setRequestError(t('page.fileFormatInvalid'));
  }`), []);
});


test('checks Promise rejection callbacks without treating then callbacks as catches', () => {
  for (const callback of [
    `(error) => { message.error(t('page.saveFailed')); }`,
    `function(error) { message.warning('Request failed'); }`,
    `(error) => message.error(t('page.saveFailed'))`,
  ]) {
    assert.equal(check(`request().catch(${callback});`).length, 1);
    assert.deepEqual(check(`request().then(${callback});`), []);
  }
});
