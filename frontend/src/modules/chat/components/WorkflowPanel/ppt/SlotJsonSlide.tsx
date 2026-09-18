import { useEffect, useRef, useState } from 'react';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { resolveCoreAssetUrl, resolveMarkdownImageUrlAsync, isExpiredSignedUrl } from '@/modules/knowledge/utils/imageUrl';
import { parseSlideSpec, type SlideSpec } from './slideSchema';
import { PPT_FONT_FACE, SAFE_FONT_STACK, resolveTheme } from './themes';

async function loadArtifactText(raw: unknown): Promise<string> {
  if (raw == null) return '';
  if (typeof raw === 'string') return raw;
  if (typeof raw !== 'object') return String(raw);
  const obj = raw as Record<string, unknown>;
  if (typeof obj.text === 'string') return obj.text;
  // save_artifact(content_type='json') → { data: <object|string> }
  if ('data' in obj) {
    if (typeof obj.data === 'string') return obj.data;
    if (obj.data && typeof obj.data === 'object') {
      try {
        return JSON.stringify(obj.data);
      } catch {
        return '';
      }
    }
  }
  // Inline slide object (no envelope).
  if ('layout' in obj && 'title' in obj) {
    try {
      return JSON.stringify(obj);
    } catch {
      return '';
    }
  }
  if (obj.path && (obj.type === 'text' || obj.type === 'json')) {
    const pathForSign = String(obj.path ?? obj.url ?? '').trim();
    const apiUrlRaw = obj.url ? String(obj.url).trim() : '';
    const apiUrl = apiUrlRaw ? resolveCoreAssetUrl(apiUrlRaw) : '';
    const fetchUrl = apiUrl && !isExpiredSignedUrl(apiUrl)
      ? apiUrl
      : await resolveMarkdownImageUrlAsync(pathForSign);
    const response = await fetch(fetchUrl);
    if (!response.ok) throw new Error('failed to load slide artifact');
    return response.text();
  }
  return '';
}

function scaleToFit(containerW: number, containerH: number): number {
  const sx = containerW / 1600;
  const sy = containerH / 900;
  return Math.max(0.2, Math.min(sx, sy, 1));
}

function SlideCanvas({ spec }: { spec: SlideSpec }) {
  const theme = resolveTheme(spec.theme);
  const cards = (spec.cards || []).slice(0, 4);
  const bullets = (spec.bullets || []).slice(0, 8);
  const kpis = (spec.kpis || []).slice(0, 4);

  return (
    <div
      className='slot-json-slide__canvas'
      style={{
        background: theme.bg,
        color: theme.text,
        fontFamily: SAFE_FONT_STACK,
      }}
    >
      <div className='slot-json-slide__wash' style={{ background: theme.wash }} />
      <div className='slot-json-slide__accent-bar' style={{ background: theme.primary }} />

      {spec.layout === 'title' && (
        <div className='slot-json-slide__center'>
          <div className='slot-json-slide__eyebrow' style={{ color: theme.accent }}>
            PRESENTATION
          </div>
          <h1 className='slot-json-slide__hero-title' style={{ color: theme.primary }}>
            {spec.title}
          </h1>
          {spec.subtitle ? (
            <p className='slot-json-slide__hero-sub' style={{ color: theme.muted }}>
              {spec.subtitle}
            </p>
          ) : null}
        </div>
      )}

      {spec.layout === 'section' && (
        <div className='slot-json-slide__center'>
          <div
            className='slot-json-slide__section-chip'
            style={{ borderColor: theme.primary, color: theme.primary }}
          >
            SECTION
          </div>
          <h1 className='slot-json-slide__hero-title' style={{ color: theme.primary }}>
            {spec.title}
          </h1>
          {spec.subtitle ? (
            <p className='slot-json-slide__hero-sub' style={{ color: theme.muted }}>
              {spec.subtitle}
            </p>
          ) : null}
        </div>
      )}

      {spec.layout === 'bullets' && (
        <div className='slot-json-slide__pad'>
          <h2 className='slot-json-slide__title' style={{ color: theme.primary }}>{spec.title}</h2>
          {spec.subtitle ? (
            <p className='slot-json-slide__subtitle' style={{ color: theme.muted }}>{spec.subtitle}</p>
          ) : null}
          <ul className='slot-json-slide__bullets'>
            {bullets.map((b, i) => (
              <li key={i}>
                <span className='slot-json-slide__dot' style={{ background: theme.accent }} />
                <span>{b}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {spec.layout === 'cards' && (
        <div className='slot-json-slide__pad'>
          <h2 className='slot-json-slide__title' style={{ color: theme.primary }}>{spec.title}</h2>
          {spec.subtitle ? (
            <p className='slot-json-slide__subtitle' style={{ color: theme.muted }}>{spec.subtitle}</p>
          ) : null}
          <div
            className='slot-json-slide__cards'
            style={{
              // 3+ cards → 2-column grid so columns stay readable at preview scale.
              gridTemplateColumns: cards.length <= 2
                ? `repeat(${Math.max(cards.length, 1)}, minmax(0, 1fr))`
                : 'repeat(2, minmax(0, 1fr))',
            }}
          >
            {cards.map((card, i) => (
              <div
                key={i}
                className='slot-json-slide__card'
                style={{
                  background: theme.cardBg,
                  borderColor: theme.cardBorder,
                }}
              >
                <div className='slot-json-slide__card-heading' style={{ color: theme.accent }}>
                  {card.heading}
                </div>
                {card.body ? <p className='slot-json-slide__card-body'>{card.body}</p> : null}
                {card.bullets?.length ? (
                  <ul className='slot-json-slide__card-bullets'>
                    {card.bullets.slice(0, 5).map((b, j) => (
                      <li key={j}>{b}</li>
                    ))}
                  </ul>
                ) : null}
              </div>
            ))}
          </div>
        </div>
      )}

      {spec.layout === 'two_column' && (
        <div className='slot-json-slide__pad'>
          <h2 className='slot-json-slide__title' style={{ color: theme.primary }}>{spec.title}</h2>
          {spec.subtitle ? (
            <p className='slot-json-slide__subtitle' style={{ color: theme.muted }}>{spec.subtitle}</p>
          ) : null}
          <div className='slot-json-slide__two-col'>
            {[spec.left, spec.right].map((col, i) => (
              <div
                key={i}
                className='slot-json-slide__col'
                style={{ background: theme.cardBg, borderColor: theme.cardBorder }}
              >
                {col?.heading ? (
                  <div className='slot-json-slide__card-heading' style={{ color: theme.accent }}>
                    {col.heading}
                  </div>
                ) : null}
                <ul className='slot-json-slide__bullets slot-json-slide__bullets--tight'>
                  {(col?.bullets || []).map((b, j) => (
                    <li key={j}>
                      <span className='slot-json-slide__dot' style={{ background: theme.accent }} />
                      <span>{b}</span>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </div>
      )}

      {spec.layout === 'kpi' && (
        <div className='slot-json-slide__pad slot-json-slide__pad--kpi'>
          <h2 className='slot-json-slide__title' style={{ color: theme.primary }}>{spec.title}</h2>
          {spec.subtitle ? (
            <p className='slot-json-slide__subtitle' style={{ color: theme.muted }}>{spec.subtitle}</p>
          ) : null}
          <div
            className='slot-json-slide__kpis'
            style={{ gridTemplateColumns: `repeat(${Math.max(kpis.length, 1)}, minmax(0, 1fr))` }}
          >
            {kpis.map((k, i) => (
              <div
                key={i}
                className='slot-json-slide__kpi'
                style={{ background: theme.cardBg, borderColor: theme.cardBorder }}
              >
                <div className='slot-json-slide__kpi-value' style={{ color: theme.accent }}>{k.value}</div>
                <div className='slot-json-slide__kpi-label' style={{ color: theme.muted }}>{k.label}</div>
              </div>
            ))}
          </div>
          {bullets.length ? (
            <ul
              className={`slot-json-slide__bullets${bullets.length >= 3 ? ' slot-json-slide__bullets--cols' : ''}`}
            >
              {bullets.map((b, i) => (
                <li key={i}>
                  <span className='slot-json-slide__dot' style={{ background: theme.accent }} />
                  <span>{b}</span>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      )}

      {spec.footer ? (
        <div className='slot-json-slide__footer' style={{ color: theme.muted }}>
          {spec.footer}
        </div>
      ) : null}
    </div>
  );
}

export function SlotJsonSlide({
  slot,
  compact = false,
}: {
  slot: SlotRevision;
  compact?: boolean;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [spec, setSpec] = useState<SlideSpec | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [scale, setScale] = useState(0.4);

  useEffect(() => {
    let cancelled = false;
    setError(null);
    // Prefer parsing the raw artifact envelope first ({ data: ... }).
    const direct = parseSlideSpec(slot.artifact_value);
    if (direct) {
      setSpec(direct);
      return () => {
        cancelled = true;
      };
    }
    loadArtifactText(slot.artifact_value)
      .then((text) => {
        if (cancelled) return;
        const parsed = parseSlideSpec(text) || parseSlideSpec(slot.artifact_value);
        if (!parsed) {
          setError('Not a valid slide JSON');
          setSpec(null);
          return;
        }
        setSpec(parsed);
      })
      .catch(() => {
        if (!cancelled) {
          setError('Failed to load slide');
          setSpec(null);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [slot.artifact_value, slot.slot_id, slot.revision]);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const update = () => {
      const rect = host.getBoundingClientRect();
      // Fit the 1600×900 canvas into the host box (host is CSS aspect-ratio 16/9).
      const w = Math.max(160, rect.width || 320);
      const h = Math.max(90, rect.height || Math.round(w * 9 / 16));
      setScale(scaleToFit(w, h));
    };
    update();
    const ro = new ResizeObserver(update);
    ro.observe(host);
    return () => ro.disconnect();
  }, [compact, spec]);

  const frameH = Math.round(900 * scale);

  if (error) {
    return <div className='slot-json-slide slot-json-slide--error'>{error}</div>;
  }
  if (!spec) {
    return <div className='slot-json-slide slot-json-slide--loading'>Loading slide…</div>;
  }

  const rootClass = [
    'slot-json-slide',
    compact ? 'slot-json-slide--compact' : '',
  ].filter(Boolean).join(' ');

  return (
    <div ref={hostRef} className={rootClass}>
      <div className='slot-json-slide__viewport' style={{ height: frameH }}>
        <div
          className='slot-json-slide__scaler'
          style={{
            width: 1600,
            height: 900,
            transform: `scale(${scale})`,
            transformOrigin: 'top left',
            transition: 'none',
          }}
        >
          <SlideCanvas spec={spec} />
        </div>
        <div className='slot-json-slide__size-hint'>
          {Math.round(1600 * scale)}×{frameH} · {PPT_FONT_FACE}
        </div>
      </div>
    </div>
  );
}
