import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useState } from 'react';
import ZoteroWebDAVAddress, { splitWebDAVAddress, webDAVURL } from './ZoteroWebDAVAddress';
afterEach(cleanup);
it.each(['https://dav.example.com/dav', 'https://dav.example.com/dav/zotero/', 'https://dav.example.com/dav/zotero'])('normalizes %s with exactly one Zotero directory', value => {
  const parts = splitWebDAVAddress(value);
  expect(webDAVURL(parts.protocol, parts.address)).toBe('https://dav.example.com/dav/zotero/');
});
it('accepts complete pasted URLs, preserves paths and does not lose a typed slash', () => {
  const changed = vi.fn();
  function Form() { const [value, setValue] = useState(''); return <><label htmlFor="zotero-webdav-url">WebDAV 地址</label><ZoteroWebDAVAddress value={value} disabled={false} onChange={url => { changed(url); setValue(url); }} /></>; }
  render(<Form />);
  const input = screen.getByLabelText('WebDAV 地址');
  fireEvent.change(input, { target: { value: 'dav.example.com/' } });
  expect(input).toHaveValue('dav.example.com/');
  fireEvent.change(input, { target: { value: 'http://dav.example.com/dav/zotero/' } });
  expect(input).toHaveValue('dav.example.com/dav');
  expect(changed).toHaveBeenLastCalledWith('http://dav.example.com/dav/zotero/');
  fireEvent.change(input, { target: { value: '' } });
  expect(changed).toHaveBeenLastCalledWith('');
});
