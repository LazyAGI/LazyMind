const EDGE_BRAND_PATTERN = /^(Microsoft Edge|Microsoft Edge WebView2)$/i;
const CHROME_BRAND_PATTERN = /^(Google Chrome|Chrome)$/i;

export function detectBrowserIdentity(source = globalThis.navigator || {}) {
  const brands = source.userAgentData?.brands
    ? Array.from(source.userAgentData.brands)
    : [];
  const edgeBrand = brands.find((item) => EDGE_BRAND_PATTERN.test(String(item?.brand || '')));
  const chromeBrand = brands.find((item) => CHROME_BRAND_PATTERN.test(String(item?.brand || '')));
  const userAgent = String(source.userAgent || '');

  let name = 'Chromium';
  let version = '';
  if (edgeBrand) {
    name = 'Microsoft Edge';
    version = String(edgeBrand.version || '');
  } else {
    const edgeMatch = userAgent.match(/Edg(?:A|iOS)?\/([\d.]+)/i);
    if (edgeMatch) {
      name = 'Microsoft Edge';
      version = edgeMatch[1];
    } else if (chromeBrand) {
      name = 'Google Chrome';
      version = String(chromeBrand.version || '');
    } else {
      const chromeMatch = userAgent.match(/(?:Chrome|CriOS)\/([\d.]+)/i);
      if (chromeMatch) {
        name = 'Google Chrome';
        version = chromeMatch[1];
      }
    }
  }

  const platform = String(
    source.userAgentData?.platform || source.platform || 'Desktop',
  ).trim() || 'Desktop';
  return {
    name,
    version,
    platform,
    deviceName: `${platform} ${name}`,
  };
}
