import agreementZhCN from '@/legal/user-agreement.zh-CN.md?raw';
import agreementEnUS from '@/legal/user-agreement.en-US.md?raw';

export function getUserAgreementMarkdown(language?: string): string {
  const normalized = (language || '').toLowerCase();
  if (normalized.startsWith('en')) {
    return agreementEnUS.trim();
  }
  return agreementZhCN.trim();
}
