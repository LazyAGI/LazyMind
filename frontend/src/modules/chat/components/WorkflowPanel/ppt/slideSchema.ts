/** Shared slide JSON contract (SSOT for preview + PPTX export). */

export type SlideLayout =
  | 'title'
  | 'section'
  | 'bullets'
  | 'cards'
  | 'two_column'
  | 'kpi';

export type ThemeId =
  | 'corporate_blue'
  | 'festival_red'
  | 'ink_wash'
  | 'dark_tech'
  | 'fresh_green'
  | 'warm_sand';

export interface SlideCard {
  heading: string;
  body?: string;
  bullets?: string[];
}

export interface SlideColumn {
  heading?: string;
  bullets: string[];
}

export interface SlideKpi {
  label: string;
  value: string;
}

export interface SlideSpec {
  layout: SlideLayout;
  theme?: ThemeId | string;
  title: string;
  subtitle?: string;
  bullets?: string[];
  cards?: SlideCard[];
  left?: SlideColumn;
  right?: SlideColumn;
  kpis?: SlideKpi[];
  footer?: string;
  /** Speaker notes / 演讲稿 (also stored in preview_notes slot for the UI). */
  notes?: string;
}

const LAYOUTS = new Set<string>([
  'title',
  'section',
  'bullets',
  'cards',
  'two_column',
  'kpi',
]);

function asString(v: unknown): string {
  return typeof v === 'string' ? v.trim() : '';
}

function asStringList(v: unknown): string[] {
  if (!Array.isArray(v)) return [];
  return v.map((x) => asString(x)).filter(Boolean);
}

function parseCard(raw: unknown): SlideCard | null {
  if (!raw || typeof raw !== 'object') return null;
  const o = raw as Record<string, unknown>;
  const heading = asString(o.heading) || asString(o.title);
  if (!heading) return null;
  return {
    heading,
    body: asString(o.body) || asString(o.content) || undefined,
    bullets: asStringList(o.bullets).length ? asStringList(o.bullets) : undefined,
  };
}

function parseColumn(raw: unknown): SlideColumn | undefined {
  if (!raw || typeof raw !== 'object') return undefined;
  const o = raw as Record<string, unknown>;
  const bullets = asStringList(o.bullets);
  if (!bullets.length) return undefined;
  return {
    heading: asString(o.heading) || asString(o.title) || undefined,
    bullets,
  };
}

function parseKpi(raw: unknown): SlideKpi | null {
  if (!raw || typeof raw !== 'object') return null;
  const o = raw as Record<string, unknown>;
  const label = asString(o.label) || asString(o.name);
  const value = asString(o.value);
  if (!label || !value) return null;
  return { label, value };
}

/** Strip markdown fences / noise and parse the first JSON object. */
export function extractJsonObjectText(raw: string): string | null {
  const text = (raw || '').trim();
  if (!text) return null;
  if (text.startsWith('{') && text.endsWith('}')) return text;

  const fenced = text.match(/```(?:json)?\s*([\s\S]*?)```/i);
  if (fenced?.[1]) {
    const inner = fenced[1].trim();
    if (inner.startsWith('{')) return inner;
  }

  const start = text.indexOf('{');
  const end = text.lastIndexOf('}');
  if (start >= 0 && end > start) return text.slice(start, end + 1);
  return null;
}

/** Unwrap common artifact envelopes and double-encoded JSON strings. */
function unwrapSlidePayload(raw: unknown, depth = 0): unknown {
  if (raw == null || depth > 5) return raw;

  if (typeof raw === 'string') {
    const trimmed = raw.trim();
    if (!trimmed) return raw;
    // Prefer whole-string JSON.parse so double-encoded quotes unwrap cleanly.
    if (trimmed.startsWith('{') || trimmed.startsWith('"') || trimmed.startsWith('[')) {
      try {
        const parsed = JSON.parse(trimmed);
        if (typeof parsed === 'string') return unwrapSlidePayload(parsed, depth + 1);
        return unwrapSlidePayload(parsed, depth + 1);
      } catch {
        // fall through to brace extraction
      }
    }
    const jsonText = extractJsonObjectText(trimmed);
    if (!jsonText || jsonText === trimmed) return raw;
    try {
      const parsed = JSON.parse(jsonText);
      if (typeof parsed === 'string') return unwrapSlidePayload(parsed, depth + 1);
      return unwrapSlidePayload(parsed, depth + 1);
    } catch {
      return raw;
    }
  }

  if (typeof raw === 'object' && !Array.isArray(raw)) {
    const maybe = raw as Record<string, unknown>;
    // save_artifact(content_type='json') stores { data: <slide|string> }
    if ('data' in maybe && !('layout' in maybe && 'title' in maybe)) {
      return unwrapSlidePayload(maybe.data, depth + 1);
    }
    // Offloaded / text wrapper: { text: "{...}" } or { text: <object> }
    if ('text' in maybe && !('layout' in maybe && 'title' in maybe)) {
      return unwrapSlidePayload(maybe.text, depth + 1);
    }
  }

  return raw;
}

export function parseSlideSpec(raw: unknown): SlideSpec | null {
  const unwrapped = unwrapSlidePayload(raw);
  let obj: Record<string, unknown> | null = null;

  if (typeof unwrapped === 'string') {
    const jsonText = extractJsonObjectText(unwrapped);
    if (!jsonText) return null;
    try {
      const parsed = JSON.parse(jsonText);
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        obj = parsed as Record<string, unknown>;
      } else if (typeof parsed === 'string') {
        return parseSlideSpec(parsed);
      }
    } catch {
      return null;
    }
  } else if (unwrapped && typeof unwrapped === 'object' && !Array.isArray(unwrapped)) {
    obj = unwrapped as Record<string, unknown>;
  }

  if (!obj) return null;

  const layoutRaw = asString(obj.layout) || 'bullets';
  const layout = (LAYOUTS.has(layoutRaw) ? layoutRaw : 'bullets') as SlideLayout;
  const title = asString(obj.title);
  if (!title) return null;

  const cards = Array.isArray(obj.cards)
    ? obj.cards.map(parseCard).filter((c): c is SlideCard => Boolean(c)).slice(0, 4)
    : undefined;
  const kpis = Array.isArray(obj.kpis)
    ? obj.kpis.map(parseKpi).filter((k): k is SlideKpi => Boolean(k)).slice(0, 4)
    : undefined;

  return {
    layout,
    theme: asString(obj.theme) || undefined,
    title,
    subtitle: asString(obj.subtitle) || undefined,
    bullets: asStringList(obj.bullets).slice(0, 8),
    cards: cards?.length ? cards : undefined,
    left: parseColumn(obj.left),
    right: parseColumn(obj.right),
    kpis: kpis?.length ? kpis : undefined,
    footer: asString(obj.footer) || undefined,
    notes: asString(obj.notes) || asString(obj.speaker_notes) || undefined,
  };
}

export function isSlideSpecArtifact(value: unknown): boolean {
  return parseSlideSpec(value) != null;
}

export function looksLikeHtmlDocument(value: string): boolean {
  const s = value.trim().toLowerCase();
  return s.includes('<html') || (s.includes('<!doctype html') && s.includes('<body'));
}
