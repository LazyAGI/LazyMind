const CACHE_PREFIX = "lazymind:pdf-temporary-chat:";
export const PDF_CHAT_CACHE_TTL_MS = 60 * 60 * 1000;

interface CachedPdfChat {
  conversationId: string;
  lastActiveAt: number;
}

function cacheKey(documentId: string) {
  return `${CACHE_PREFIX}${documentId}`;
}

export function readCachedPdfChat(documentId: string, now = Date.now()): CachedPdfChat | null {
  try {
    const raw = window.localStorage.getItem(cacheKey(documentId));
    if (!raw) return null;
    const cached = JSON.parse(raw) as CachedPdfChat;
    if (!cached.conversationId || now - cached.lastActiveAt > PDF_CHAT_CACHE_TTL_MS) {
      window.localStorage.removeItem(cacheKey(documentId));
      return null;
    }
    return cached;
  } catch {
    return null;
  }
}

export function touchCachedPdfChat(documentId: string, conversationId: string, now = Date.now()) {
  if (!documentId || !conversationId) return;
  try {
    window.localStorage.setItem(cacheKey(documentId), JSON.stringify({ conversationId, lastActiveAt: now }));
  } catch {
    // Chat remains available from the server when browser storage is unavailable.
  }
}
