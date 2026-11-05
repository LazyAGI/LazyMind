import agreementZhCN from '@/legal/user-agreement.zh-CN.md?raw';
import agreementEnUS from '@/legal/user-agreement.en-US.md?raw';
import { USER_AGREEMENT_VERSION } from './consent';

export function getUserAgreementMarkdown(language?: string): string {
  const normalized = (language || '').toLowerCase();
  const agreement = normalized.startsWith('en') ? agreementEnUS : agreementZhCN;
  return agreement.trim().replaceAll('{{USER_AGREEMENT_VERSION}}', USER_AGREEMENT_VERSION);
}
