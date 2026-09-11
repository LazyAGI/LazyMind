const DEFAULT_CAPTURE_CHARS = 200_000;
const MAX_CAPTURE_CHARS = 400_000;
const MAX_IMAGES = 1000;
const MAX_LINKS = 2000;
const MAX_METADATA_TEXT_CHARS = 500;
const MAX_METADATA_URL_CHARS = 2048;

export async function captureCurrentPage(options = {}) {
  const [tab] = await chrome.tabs.query({active: true, lastFocusedWindow: true});
  if (!tab?.id) {
    throw browserError('NO_ACTIVE_TAB', '没有可抓取的当前标签页');
  }
  if (!isSupportedPageURL(tab.url || '')) {
    throw browserError('UNSUPPORTED_PAGE', '浏览器内部页、商店页和扩展页不支持抓取');
  }
  const offset = normalizeNonNegativeInteger(options.offset, 0);
  const maxChars = Math.min(
    normalizeNonNegativeInteger(options.max_chars, DEFAULT_CAPTURE_CHARS) || DEFAULT_CAPTURE_CHARS,
    MAX_CAPTURE_CHARS,
  );
  try {
    const [execution] = await chrome.scripting.executeScript({
      target: {tabId: tab.id, allFrames: false},
      func: extractPageInTab,
      args: [
        offset,
        maxChars,
        MAX_IMAGES,
        MAX_LINKS,
        MAX_METADATA_TEXT_CHARS,
        MAX_METADATA_URL_CHARS,
      ],
    });
    if (!execution?.result) {
      throw browserError('CAPTURE_EMPTY', '页面没有返回可抓取内容');
    }
    return {
      untrusted_browser_content: true,
      ...execution.result,
    };
  } catch (error) {
    if (error?.code) throw error;
    throw browserError(
      'SITE_PERMISSION_REQUIRED',
      '网页读取权限尚未开启。请点击 LazyMind Browser 扩展并选择“开启网页读取”。',
      {cause: String(error?.message || error)},
    );
  }
}

export function chunkText(value, rawOffset, rawMaxChars) {
  const text = String(value || '');
  let start = Math.min(normalizeNonNegativeInteger(rawOffset, 0), text.length);
  const maxChars = Math.max(1, normalizeNonNegativeInteger(rawMaxChars, DEFAULT_CAPTURE_CHARS));
  if (start > 0 && isLowSurrogate(text.charCodeAt(start)) && isHighSurrogate(text.charCodeAt(start - 1))) {
    start += 1;
  }
  let end = Math.min(start + maxChars, text.length);
  if (end < text.length && isHighSurrogate(text.charCodeAt(end - 1)) && isLowSurrogate(text.charCodeAt(end))) {
    end -= 1;
  }
  return {
    text: text.slice(start, end),
    offset: start,
    nextOffset: end < text.length ? end : null,
    complete: end >= text.length,
  };
}

export function isSupportedPageURL(rawURL) {
  try {
    const parsed = new URL(rawURL);
    return parsed.protocol === 'http:' || parsed.protocol === 'https:';
  } catch {
    return false;
  }
}

export function originPattern(rawURL) {
  const parsed = new URL(rawURL);
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw browserError('UNSUPPORTED_PAGE', '当前页面不支持站点授权');
  }
  return `${parsed.origin}/*`;
}

function browserError(code, message, details) {
  const error = new Error(message);
  error.code = code;
  error.details = details;
  return error;
}

function normalizeNonNegativeInteger(value, fallback) {
  const number = Number(value);
  if (!Number.isFinite(number)) return fallback;
  return Math.max(0, Math.floor(number));
}

function isHighSurrogate(code) {
  return code >= 0xD800 && code <= 0xDBFF;
}

function isLowSurrogate(code) {
  return code >= 0xDC00 && code <= 0xDFFF;
}

async function extractPageInTab(
  offset,
  maxChars,
  maxImages,
  maxLinks,
  maxMetadataTextChars,
  maxMetadataURLChars,
) {
  const cleanText = (value) => String(value || '')
    .replace(/\u0000/g, '')
    .replace(/[ \t]+\n/g, '\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
  const isHighSurrogateCode = (code) => code >= 0xD800 && code <= 0xDBFF;
  const isLowSurrogateCode = (code) => code >= 0xDC00 && code <= 0xDFFF;
  const sliceTextChunk = (text, rawOffset, rawMaxChars) => {
    let start = Math.min(Math.max(0, Math.floor(Number(rawOffset) || 0)), text.length);
    const chunkChars = Math.max(1, Math.floor(Number(rawMaxChars) || 1));
    if (start > 0 && isLowSurrogateCode(text.charCodeAt(start))
        && isHighSurrogateCode(text.charCodeAt(start - 1))) {
      start += 1;
    }
    let end = Math.min(start + chunkChars, text.length);
    if (end < text.length && isHighSurrogateCode(text.charCodeAt(end - 1))
        && isLowSurrogateCode(text.charCodeAt(end))) {
      end -= 1;
    }
    return {
      text: text.slice(start, end),
      offset: start,
      nextOffset: end < text.length ? end : null,
      complete: end >= text.length,
    };
  };
  const limitations = [];
  const body = document.body;
  let visibleText = cleanText(body?.innerText || '');

  for (const frame of document.querySelectorAll('iframe')) {
    try {
      if (!frame.contentDocument) {
        limitations.push('cross_origin_iframe_not_captured');
        continue;
      }
      const frameText = cleanText(frame.contentDocument.body?.innerText || '');
      if (frameText) visibleText += `\n\n${frameText}`;
    } catch {
      limitations.push('cross_origin_iframe_not_captured');
    }
  }

  const articleCandidate = document.querySelector(
    'article, main, [role="main"], [itemprop="articleBody"]',
  );
  let articleText = cleanText(articleCandidate?.innerText || '');
  if (!articleText && body) {
    const clone = body.cloneNode(true);
    clone.querySelectorAll(
      'script, style, noscript, template, nav, footer, header, aside, form',
    ).forEach((node) => node.remove());
    articleText = cleanText(clone.textContent || '');
  }

  const imagesAll = Array.from(document.images);
  const linksAll = Array.from(document.querySelectorAll('a[href]'));
  if (imagesAll.length > maxImages) limitations.push('images_truncated');
  if (linksAll.length > maxLinks) limitations.push('links_truncated');

  const totalChars = visibleText.length;
  const encoder = new TextEncoder();
  const digest = await crypto.subtle.digest('SHA-256', encoder.encode(visibleText));
  const sha256 = Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('');
  const visibleChunk = sliceTextChunk(visibleText, offset, maxChars);
  const articleChunk = sliceTextChunk(articleText, 0, maxChars);
  const truncated = !visibleChunk.complete;
  if (truncated) limitations.push('visible_text_chunked');
  if (articleText.length > maxChars) {
    limitations.push('article_text_first_chunk_only');
  }

  return {
    schema_version: '1',
    capture_id: crypto.randomUUID(),
    tab: {
      url: location.href,
      origin: location.origin,
      title: document.title,
      captured_at: new Date().toISOString(),
    },
    content: {
      article_text: offset === 0 ? articleChunk.text : '',
      visible_text: visibleChunk.text,
      lang: document.documentElement.lang || navigator.language || '',
      total_chars: totalChars,
      returned_chars: visibleChunk.text.length,
      offset: visibleChunk.offset,
      next_offset: visibleChunk.nextOffset,
      complete: visibleChunk.complete,
      truncated,
      sha256,
    },
    images: imagesAll.slice(0, maxImages).map((image) => ({
      alt: cleanText(image.alt).slice(0, maxMetadataTextChars),
      src: String(image.currentSrc || image.src || '').slice(0, maxMetadataURLChars),
    })),
    links: linksAll.slice(0, maxLinks).map((link) => ({
      text: cleanText(link.innerText || link.getAttribute('aria-label') || '')
        .slice(0, maxMetadataTextChars),
      href: String(link.href || '').slice(0, maxMetadataURLChars),
    })),
    limitations: Array.from(new Set(limitations)),
  };
}
