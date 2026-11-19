import type { AnchorHTMLAttributes } from 'react';
import { externalLinkProps } from '@/runtime/externalNavigation';
import './index.scss';

export default function ExternalLink({ href, download, onClick, onAuxClick, title, ...props }: AnchorHTMLAttributes<HTMLAnchorElement>) {
  const link = externalLinkProps(href);
  if (download !== undefined) return <a {...props} href={link.href} download={download} title={title} onClick={onClick} onAuxClick={onAuxClick} />;
  return <a {...props} {...link} title={[title, link.title].filter(Boolean).join(' · ') || undefined}
    onClick={event => { onClick?.(event); link.onClick?.(event); }}
    onAuxClick={event => { onAuxClick?.(event); link.onAuxClick?.(event); }} />;
}
