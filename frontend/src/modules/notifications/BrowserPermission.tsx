import { useEffect, useState } from 'react';
import { Button } from 'antd';
import { useTranslation } from 'react-i18next';
import { browserNotificationsSupported } from './browser';

export default function BrowserPermission() {
  const { t } = useTranslation();
  const supported = browserNotificationsSupported();
  const [permission, setPermission] = useState(() => supported ? Notification.permission : 'default');
  const [requesting, setRequesting] = useState(false);
  useEffect(() => {
    const update = () => { if (supported) setPermission(Notification.permission); };
    window.addEventListener('focus', update);
    return () => window.removeEventListener('focus', update);
  }, [supported]);
  if (!supported) return <p>{t('notifications.browserUnsupported')}</p>;
  return <div className="notification-browser-permission">
    <p>{t('notifications.browserHint')}</p>
    <p>{t(`notifications.browserPermission_${permission}`)}</p>
    {permission === 'default' && <Button loading={requesting} onClick={async () => {
      setRequesting(true);
      try {
        setPermission(await Notification.requestPermission());
        window.dispatchEvent(new Event('lazymind:notification-permission-change'));
      }
      catch { setPermission(Notification.permission); }
      finally { setRequesting(false); }
    }}>{t('notifications.browserAuthorize')}</Button>}
  </div>;
}
