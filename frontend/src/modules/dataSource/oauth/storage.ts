import type {
  CloudDataSourceOAuthMessage,
  CloudDataSourceProvider,
  FeishuDataSourceOAuthMessage,
  FeishuDataSourceWizardDraft,
  FeishuPendingOAuthSession,
} from "./types";

const RESULT_STORAGE_KEY = "lazymind:datasource:feishu-oauth:result";
const DRAFT_STORAGE_KEY = "lazymind:datasource:feishu-oauth:draft";
const PENDING_STORAGE_KEY = "lazymind:datasource:feishu-oauth:pending";
const PENDING_STORAGE_KEY_PREFIX = `${PENDING_STORAGE_KEY}:`;

function getProviderStorageKey(baseKey: string, provider: CloudDataSourceProvider) {
  return provider === "feishu" ? baseKey : baseKey.replace(":feishu-oauth", `:${provider}-oauth`);
}

function savePendingFeishuOAuthSession(payload: FeishuPendingOAuthSession) {
  const serialized = JSON.stringify(payload);
  sessionStorage.setItem(PENDING_STORAGE_KEY, serialized);
  sessionStorage.setItem(`${PENDING_STORAGE_KEY_PREFIX}${payload.state}`, serialized);
}

function parsePendingFeishuOAuthSession(raw: string | null) {
  if (!raw) {
    return null;
  }

  try {
    return JSON.parse(raw) as FeishuPendingOAuthSession;
  } catch {
    return null;
  }
}

function loadPendingFeishuOAuthSession(state: string) {
  const pendingByState = parsePendingFeishuOAuthSession(
    sessionStorage.getItem(`${PENDING_STORAGE_KEY_PREFIX}${state}`),
  );

  if (pendingByState?.state === state) {
    return pendingByState;
  }

  const pending = parsePendingFeishuOAuthSession(
    sessionStorage.getItem(PENDING_STORAGE_KEY),
  );

  if (pending?.state === state) {
    return pending;
  }

  return null;
}

export function savePendingCloudOAuthSession(
  provider: CloudDataSourceProvider,
  payload: FeishuPendingOAuthSession,
) {
  if (provider === "feishu") {
    savePendingFeishuOAuthSession({ ...payload, provider });
    return;
  }

  const storageKey = getProviderStorageKey(PENDING_STORAGE_KEY, provider);
  const storageKeyPrefix = `${storageKey}:`;
  const serialized = JSON.stringify({ ...payload, provider });
  sessionStorage.setItem(storageKey, serialized);
  sessionStorage.setItem(`${storageKeyPrefix}${payload.state}`, serialized);
}

export function loadPendingCloudOAuthSession(
  provider: CloudDataSourceProvider,
  state: string,
) {
  if (provider === "feishu") {
    return loadPendingFeishuOAuthSession(state);
  }

  const storageKey = getProviderStorageKey(PENDING_STORAGE_KEY, provider);
  const storageKeyPrefix = `${storageKey}:`;
  const pendingByState = parsePendingFeishuOAuthSession(
    sessionStorage.getItem(`${storageKeyPrefix}${state}`),
  );

  if (pendingByState?.state === state) {
    return pendingByState;
  }

  const pending = parsePendingFeishuOAuthSession(sessionStorage.getItem(storageKey));
  if (pending?.state === state) {
    return pending;
  }

  return null;
}

export function clearPendingCloudOAuthSession(
  provider: CloudDataSourceProvider,
  state: string,
) {
  if (provider === "feishu") {
    clearPendingFeishuOAuthSession(state);
    return;
  }

  const storageKey = getProviderStorageKey(PENDING_STORAGE_KEY, provider);
  const storageKeyPrefix = `${storageKey}:`;
  sessionStorage.removeItem(`${storageKeyPrefix}${state}`);
  const pending = parsePendingFeishuOAuthSession(sessionStorage.getItem(storageKey));

  if (!pending || pending.state === state) {
    sessionStorage.removeItem(storageKey);
  }
}

function clearPendingFeishuOAuthSession(state: string) {
  sessionStorage.removeItem(`${PENDING_STORAGE_KEY_PREFIX}${state}`);
  const pending = parsePendingFeishuOAuthSession(
    sessionStorage.getItem(PENDING_STORAGE_KEY),
  );

  if (!pending || pending.state === state) {
    sessionStorage.removeItem(PENDING_STORAGE_KEY);
  }
}

export function saveFeishuDataSourceOAuthResult(
  payload: FeishuDataSourceOAuthMessage,
) {
  const provider =
    payload.status === "success" ? payload.connection.provider : payload.provider || "feishu";
  saveCloudDataSourceOAuthResult(provider, payload);
}

export function saveCloudDataSourceOAuthResult(
  provider: CloudDataSourceProvider,
  payload: CloudDataSourceOAuthMessage,
) {
  sessionStorage.setItem(
    getProviderStorageKey(RESULT_STORAGE_KEY, provider),
    JSON.stringify(payload),
  );
}

export function consumeFeishuDataSourceOAuthResult() {
  return consumeCloudDataSourceOAuthResult("feishu");
}

export function consumeCloudDataSourceOAuthResult(provider: CloudDataSourceProvider) {
  const storageKey = getProviderStorageKey(RESULT_STORAGE_KEY, provider);
  const raw = sessionStorage.getItem(storageKey);
  if (!raw) {
    return null;
  }

  sessionStorage.removeItem(storageKey);

  try {
    return JSON.parse(raw) as CloudDataSourceOAuthMessage;
  } catch {
    return null;
  }
}

export function saveFeishuDataSourceWizardDraft(
  payload: FeishuDataSourceWizardDraft,
) {
  sessionStorage.setItem(DRAFT_STORAGE_KEY, JSON.stringify(payload));
}

export function clearFeishuDataSourceWizardDraft() {
  sessionStorage.removeItem(DRAFT_STORAGE_KEY);
}

export function consumeFeishuDataSourceWizardDraft() {
  const raw = sessionStorage.getItem(DRAFT_STORAGE_KEY);
  if (!raw) {
    return null;
  }

  sessionStorage.removeItem(DRAFT_STORAGE_KEY);

  try {
    return JSON.parse(raw) as FeishuDataSourceWizardDraft;
  } catch {
    return null;
  }
}
