import { useEffect, useState } from 'react';
import { getDesktopAppVersion } from './desktopBridge';

export function useAppVersion(): string | undefined {
  const [appVersion, setAppVersion] = useState<string>();

  useEffect(() => {
    let active = true;
    void getDesktopAppVersion().then((version) => {
      const rawVersion = version?.trim() || import.meta.env.VITE_APP_VERSION?.trim();
      if (active) setAppVersion(rawVersion?.match(/^v?(\d+\.\d+)/i)?.[1]);
    });
    return () => { active = false; };
  }, []);

  return appVersion;
}
