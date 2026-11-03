import { useRef, useState } from 'react';
import { Alert, Button, Modal } from 'antd';
import type { TFunction } from 'i18next';
import { openNotificationSettings } from '@/runtime/desktopBridge';
import { isDesktopRuntime } from '@/runtime/mode';
import BrowserPermission from './BrowserPermission';
import { desktopNotificationsAuthorized } from './browser';

export function NativeDesktopPermission({ t }: { t: TFunction }) {
  const [opening, setOpening] = useState(false);
  const [failed, setFailed] = useState(false);
  const pending = useRef(false);
  const openSettings = async () => {
    if (pending.current) return;
    pending.current = true;
    setOpening(true);
    setFailed(false);
    try {
      const result = await openNotificationSettings();
      setFailed(!result.ok);
    } catch {
      setFailed(true);
    } finally {
      pending.current = false;
      setOpening(false);
    }
  };
  return <>
    <p>{t('notifications.nativeDesktopHint')}</p>
    <Button type="primary" loading={opening} onClick={() => void openSettings()}>
      {t('notifications.openSystemNotificationSettings')}
    </Button>
    {failed && <Alert type="warning" showIcon role="alert" message={t('notifications.openSystemNotificationSettingsFailed')} />}
  </>;
}

// Call only after the desktop notification gate has been successfully enabled.
// Browser permission never describes the native Electron notification consumer.
export function showDesktopNotificationPermission(t: TFunction) {
  const nativeDesktop = isDesktopRuntime();
  if (!nativeDesktop && desktopNotificationsAuthorized()) return;

  return Modal.info({
    zIndex: 1700,
    title: t('notifications.desktopPermissionTitle'),
    content: nativeDesktop ? <NativeDesktopPermission t={t} /> : <BrowserPermission />,
    okText: t('notifications.desktopPermissionDone'),
  });
}
