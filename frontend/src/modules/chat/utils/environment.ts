export function buildEnvironmentContext(locale?: string) {
  return {
    locale: locale || "zh-CN",
    time: {
      now: new Date().toISOString(),
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    },
  };
}
