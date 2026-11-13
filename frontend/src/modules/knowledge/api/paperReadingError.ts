import i18n from '@/i18n';

// Axios can hit the same deadline before Core's classified response arrives.
export function paperReadingErrorMessage(error: unknown, language: string): string | undefined {
  const failure = error as {code?: string; response?: {status?: number; data?: {data?: {error_code?: string; error_message?: string}}}};
  const detail = failure?.response?.data?.data;
  if (failure?.code === 'ECONNABORTED' || failure?.code === 'ETIMEDOUT'
    || failure?.response?.status === 504 || detail?.error_code === 'request_timeout') {
    return i18n.t('pdfReader.timeout', {lng: language.startsWith('zh') ? 'zh-CN' : 'en-US'});
  }
  return typeof detail?.error_message === 'string' ? detail.error_message : undefined;
}
