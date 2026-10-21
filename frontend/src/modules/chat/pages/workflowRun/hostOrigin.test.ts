import { describe, expect, it } from 'vitest';
import { workflowHostOrigin } from './hostOrigin';

describe('workflow parent origin', () => {
  it.each(['dsh-app://app', 'dsh-app://app/'])('preserves the Desktop origin %s', value => {
    expect(workflowHostOrigin(value)).toBe('dsh-app://app');
  });
  it.each([
    null, '', 'null', '*', 'file:///app', 'data:text/html,app',
    'dsh-app://shell', 'dsh-app://app.evil.test', 'dsh-app://app:123',
    'dsh-app://user@app', 'dsh-app://app/path', 'dsh-app://app?origin=evil',
  ])('rejects untrusted or opaque origins %s', value => {
    expect(workflowHostOrigin(value)).toBeUndefined();
  });
  it('retains Web origin normalization', () => {
    expect(workflowHostOrigin('http://127.0.0.1:3080/')).toBe('http://127.0.0.1:3080');
    expect(workflowHostOrigin('https://dsh.example.test/path')).toBe('https://dsh.example.test');
  });
});
