import {
  fetchUserUiPreferences,
  patchUserUiPreferences,
} from '@/modules/user/uiPreferencesApi';

/** Bump when the legal text changes so users must re-accept. */
export const USER_AGREEMENT_VERSION = 'V0.2';
const USER_AGREEMENT_READ_SESSION_KEY = 'lazymind:user-agreement-read';

export function isAcceptedUserAgreementVersion(version?: string | null): boolean {
  return String(version || '').trim() === USER_AGREEMENT_VERSION;
}

export async function syncUserAgreementFromServer(): Promise<boolean> {
  try {
    const prefs = await fetchUserUiPreferences();
    return isAcceptedUserAgreementVersion(prefs.accepted_user_agreement_version);
  } catch (error) {
    console.error('Failed to sync user agreement from server:', error);
    // Fail closed: keep showing the consent modal until the server confirms.
    return false;
  }
}

export async function persistUserAgreementAccepted(): Promise<void> {
  await patchUserUiPreferences({
    accepted_user_agreement_version: USER_AGREEMENT_VERSION,
  });
}

export function markUserAgreementRead(): void {
  try {
    sessionStorage.setItem(USER_AGREEMENT_READ_SESSION_KEY, '1');
  } catch {
    // The user can still select the consent checkbox manually.
  }
}

export function consumeUserAgreementReadFlag(): boolean {
  try {
    const read = sessionStorage.getItem(USER_AGREEMENT_READ_SESSION_KEY) === '1';
    sessionStorage.removeItem(USER_AGREEMENT_READ_SESSION_KEY);
    return read;
  } catch {
    return false;
  }
}
