import type { MouseEvent } from 'react';
import { Button, notification } from 'antd';
import i18n from 'i18next';
import { getExternalBrowserBridge } from './desktopBridge';

function copy() {
  return /^en/i.test(i18n.language || '') ? {
    desktop: 'Open in default browser', web: 'Open in a new tab',
    failed: 'Unable to open the browser. Please try again.', retry: 'Retry',
    copy: 'Copy link', copied: 'Link copied', copyFailed: 'Unable to copy the link. Please try again.',
  } : {
    desktop: '在默认浏览器中打开', web: '在新标签页中打开',
    failed: '无法打开浏览器，请重试', retry: '重试',
    copy: '复制链接', copied: '链接已复制', copyFailed: '无法复制链接，请重试',
  };
}

export function externalWebURL(href?: string): string | undefined {
  if (!href || /[\u0000-\u001f\u007f]/.test(href)) return;
  try {
    const url = new URL(href, window.location.href);
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.origin === window.location.origin) return;
    return url.href;
  } catch { return; }
}

function safeHref(href?: string) {
  if (!href || /[\u0000-\u001f\u007f]/.test(href)) return undefined;
  try {
    const url = new URL(href, window.location.href);
    return ['http:', 'https:', 'mailto:', 'obsidian:', 'cursor:', 'blob:'].includes(url.protocol) && !url.username && !url.password ? href : undefined;
  } catch { return undefined; }
}

const pending = new Map<string, Promise<void>>();

function reportFailure(url: string) {
  const text = copy();
  notification.error({
    key: 'external-browser', message: text.failed, duration: 0,
    actions: <span style={{ display: 'flex', gap: 12 }}>
      <Button size='small' onClick={() => { notification.destroy('external-browser'); void openExternalUrl(url); }}>{text.retry}</Button>
      <Button size='small' onClick={async () => {
        try {
          await navigator.clipboard.writeText(url);
          notification.success({ key: 'external-browser-copy', message: text.copied });
        } catch {
          notification.error({ key: 'external-browser-copy', message: text.copyFailed });
        }
      }}>{text.copy}</Button>
    </span>,
  });
}

/** Opens only ordinary external web URLs. Dedicated downloads/auth flows keep their own handlers. */
export function openExternalUrl(href: string): Promise<void> {
  const url = externalWebURL(href);
  if (!url) return Promise.resolve();
  const existing = pending.get(url);
  if (existing) return existing;
  const operation = (async () => {
    try {
      const desktop = getExternalBrowserBridge();
      if (desktop) await desktop(url);
      else {
        // Create synchronously within the user gesture, sever opener before navigating.
        const popup = window.open('about:blank', '_blank');
        if (!popup) throw new Error('browser-unavailable');
        popup.opener = null;
        const policy = popup.document.createElement('meta');
        policy.name = 'referrer'; policy.content = 'no-referrer';
        popup.document.head.appendChild(policy);
        popup.location.replace(url);
      }
    } catch { reportFailure(url); }
  })();
  pending.set(url, operation);
  void operation.finally(() => { pending.delete(url); });
  return operation;
}

/** Also usable on existing Ant Design link buttons without changing their layout. */
export function externalLinkProps(href?: string) {
  const url = externalWebURL(href);
  const description = getExternalBrowserBridge() ? copy().desktop : copy().web;
  const open = (event: MouseEvent<HTMLElement>) => {
    if (!url || event.defaultPrevented || !getExternalBrowserBridge() || event.button > 1) return;
    event.preventDefault();
    void openExternalUrl(url);
  };
  return {
    href: safeHref(href),
    ...(url ? {
      target: '_blank', rel: 'noopener noreferrer', title: description,
      'aria-description': description, 'data-external-link': true,
      onClick: open, onAuxClick: open,
    } : {}),
  };
}
