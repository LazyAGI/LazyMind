import { useEffect, useState } from 'react';
import { Input, Select } from 'antd';

export function splitWebDAVAddress(value: string) {
  const match = value.trim().match(/^(https?):\/\//i);
  return { protocol: match?.[1].toLowerCase() || 'https', address: value.trim().replace(/^https?:\/\//i, '').replace(/\/+$/, '').replace(/\/zotero$/i, '') };
}
export function webDAVURL(protocol: string, address: string) {
  const base = splitWebDAVAddress(address).address;
  return base ? `${protocol}://${base}/zotero/` : '';
}

export default function ZoteroWebDAVAddress({ value, disabled, onChange }: { value: string; disabled: boolean; onChange: (url: string) => void }) {
  const [parts, setParts] = useState(() => splitWebDAVAddress(value));
  useEffect(() => {
    setParts(current => value === webDAVURL(current.protocol, current.address) ? current : splitWebDAVAddress(value));
  }, [value]); // Preserve in-progress typing when the parent echoes the normalized URL.
  const update = (next: typeof parts) => { setParts(next); onChange(webDAVURL(next.protocol, next.address)); };
  return <div className="zotero-webdav-address">
    <Select aria-label="WebDAV 协议" value={parts.protocol} disabled={disabled} options={[{ value: 'https', label: 'https' }, { value: 'http', label: 'http' }]} onChange={protocol => update({ ...parts, protocol })} />
    <span aria-hidden>://</span>
    <Input id="zotero-webdav-url" value={parts.address} placeholder="dav.example.com/dav" disabled={disabled} autoComplete="off"
      onChange={event => { const raw = event.target.value; update(/^https?:\/\//i.test(raw.trim()) ? splitWebDAVAddress(raw) : { ...parts, address: raw }); }}
      onBlur={() => update({ ...parts, address: splitWebDAVAddress(parts.address).address })} />
    <span className="zotero-webdav-suffix">/zotero/</span>
  </div>;
}
