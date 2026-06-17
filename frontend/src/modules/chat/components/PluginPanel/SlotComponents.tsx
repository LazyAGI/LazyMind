import type { SlotRevision } from "@/modules/chat/store/pluginPanel";
import { resolveCoreAssetUrl } from "@/modules/knowledge/utils/imageUrl";

/**
 * Normalize the content_type returned by the Python backend.
 * Python stores short forms: 'text', 'json', 'image', 'file', 'file_list'.
 */
function normalizeContentType(ct: string): 'image' | 'file' | 'text' {
  if (ct === 'image' || ct.startsWith('image/')) return 'image';
  if (ct === 'file' || ct === 'file_list' || ct.startsWith('application/')) return 'file';
  return 'text';
}

/** Shown when the slot has no artifact yet (backend returned no artifact_value). */
function SlotPending({ type, cardMode }: { type: 'image' | 'file' | 'text'; cardMode?: boolean }) {
  if (type === 'image') {
    return (
      <div className={`plugin-slot plugin-slot--image plugin-slot--pending${cardMode ? ' plugin-slot--image-card' : ''}`}>
        <span className='plugin-slot__placeholder-icon' aria-hidden='true'>🖼</span>
        <span className='plugin-slot__placeholder'>进行中…</span>
      </div>
    );
  }
  if (type === 'file') {
    return (
      <div className='plugin-slot plugin-slot--file plugin-slot--pending'>
        <span className='plugin-slot__placeholder'>待生成…</span>
      </div>
    );
  }
  return (
    <div className='plugin-slot plugin-slot--text plugin-slot--pending'>
      <p className='plugin-slot__text plugin-slot__text--pending'>待计算…</p>
    </div>
  );
}

export function SlotText({ slot }: { slot: SlotRevision }) {
  const raw = slot.artifact_value;
  let text: string;
  if (raw?.text !== undefined) {
    text = String(raw.text);
  } else if (raw?.data !== undefined) {
    text = typeof raw.data === 'string' ? raw.data : JSON.stringify(raw.data, null, 2);
  } else if (raw !== undefined && raw !== null) {
    text = JSON.stringify(raw);
  } else {
    return <SlotPending type='text' />;
  }
  return (
    <div className='plugin-slot plugin-slot--text'>
      <p className='plugin-slot__text'>{text}</p>
    </div>
  );
}

/**
 * SlotImage renders a single image slot.
 * cardMode=true uses the card layout (image on top, caption overlay).
 */
export function SlotImage({ slot, cardMode = false }: { slot: SlotRevision; cardMode?: boolean }) {
  const raw = slot.artifact_value;
  const url: string = raw?.url || (raw?.path ? resolveCoreAssetUrl(raw.path) : '');
  const alt: string = raw?.alt ?? '';

  if (!url) return <SlotPending type='image' cardMode={cardMode} />;

  if (cardMode) {
    return (
      <div className='plugin-slot plugin-slot--image-card'>
        <img src={url} alt={alt} className='plugin-slot__image-card-img' loading='lazy' />
        {alt && <div className='plugin-slot__image-card-caption'>{alt}</div>}
      </div>
    );
  }
  return (
    <div className='plugin-slot plugin-slot--image'>
      <img src={url} alt={alt} className='plugin-slot__image' loading='lazy' />
    </div>
  );
}

export function SlotFile({ slot }: { slot: SlotRevision }) {
  const raw = slot.artifact_value;
  const url: string = raw?.url || (raw?.path ? resolveCoreAssetUrl(raw.path) : '');
  const name: string = raw?.filename ?? raw?.name ?? slot.artifact_key;
  const size: number | undefined = raw?.size;

  if (!url) return <SlotPending type='file' />;

  return (
    <div className='plugin-slot plugin-slot--file'>
      <a
        href={url}
        download={name}
        target='_blank'
        rel='noopener noreferrer'
        className='plugin-slot__file-link'
        aria-label={`Download ${name}`}
      >
        <span className='plugin-slot__file-icon' aria-hidden='true'>📄</span>
        <span className='plugin-slot__file-name'>{name}</span>
        {size !== undefined && (
          <span className='plugin-slot__file-size'>({(size / 1024).toFixed(1)} KB)</span>
        )}
      </a>
    </div>
  );
}

/**
 * SlotRenderer dispatches to the correct slot component based on the artifact
 * content_type returned by the backend.
 * When artifact_value is absent (step not yet complete), shows a pending placeholder.
 * expectedType drives the placeholder appearance before the artifact arrives.
 */
export function SlotRenderer({
  slot,
  cardMode = false,
  expectedType,
}: {
  slot: SlotRevision;
  cardMode?: boolean;
  expectedType?: 'image' | 'file' | 'text';
}) {
  if (slot.artifact_value === undefined || slot.artifact_value === null) {
    return <SlotPending type={expectedType ?? 'text'} cardMode={cardMode} />;
  }

  const normalized = normalizeContentType(slot.content_type ?? 'text');
  if (normalized === 'image') return <SlotImage slot={slot} cardMode={cardMode} />;
  if (normalized === 'file') return <SlotFile slot={slot} />;
  return <SlotText slot={slot} />;
}
