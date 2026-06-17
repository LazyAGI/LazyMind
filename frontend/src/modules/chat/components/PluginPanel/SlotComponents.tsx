import { useTaskCenterStore, type TaskArtifact } from "@/modules/chat/store/taskCenter";
import type { SlotRevision } from "@/modules/chat/store/pluginPanel";
import { resolveCoreAssetUrl } from "@/modules/knowledge/utils/imageUrl";

/**
 * Hook to find the matching artifact from taskCenter store for a given slot.
 * Subscribes to store changes so the component re-renders when artifacts load.
 */
function useSlotArtifact(artifact_key: string, conversationId: string): TaskArtifact | undefined {
  return useTaskCenterStore((state) => {
    const tasks = state.tasksByConversation[conversationId] ?? [];
    for (const task of tasks) {
      const match = task.artifacts.find((a) => a.artifact_key === artifact_key);
      if (match) return match;
    }
    return undefined;
  });
}

export function SlotText({
  conversationId,
  slot,
}: {
  conversationId: string;
  slot: SlotRevision;
}) {
  const artifact = useSlotArtifact(slot.artifact_key, conversationId);
  // Python stores text as {text: "..."}, json as {data: ...}
  const rawValue = artifact?.value as any;
  let text: string;
  if (rawValue?.text !== undefined) {
    text = String(rawValue.text);
  } else if (rawValue?.data !== undefined) {
    text = typeof rawValue.data === 'string' ? rawValue.data : JSON.stringify(rawValue.data, null, 2);
  } else if (artifact) {
    text = JSON.stringify(rawValue);
  } else {
    // artifact not in store — shouldn't reach here via SlotRenderer, but guard anyway
    return (
      <div className='plugin-slot plugin-slot--text plugin-slot--pending'>
        <p className='plugin-slot__text plugin-slot__text--pending'>待计算…</p>
      </div>
    );
  }
  return (
    <div className="plugin-slot plugin-slot--text">
      <p className="plugin-slot__text">{text}</p>
    </div>
  );
}

/**
 * SlotImage renders a single image slot.
 * cardMode=true: card layout with image on top and caption overlay at bottom.
 */
export function SlotImage({
  conversationId,
  slot,
  cardMode = false,
}: {
  conversationId: string;
  slot: SlotRevision;
  cardMode?: boolean;
}) {
  const artifact = useSlotArtifact(slot.artifact_key, conversationId);
  // Python stores image as {path: "relative/path"} or possibly {url: "https://..."}
  const rawValue = artifact?.value as any;
  const url: string = rawValue?.url || (rawValue?.path ? resolveCoreAssetUrl(rawValue.path) : '');
  const alt: string = rawValue?.alt ?? '';

  if (!url) {
    return (
      <div className={`plugin-slot plugin-slot--image plugin-slot--empty${cardMode ? " plugin-slot--image-card" : ""}`}>
        <span className="plugin-slot__placeholder">Image pending…</span>
      </div>
    );
  }

  if (cardMode) {
    return (
      <div className="plugin-slot plugin-slot--image-card">
        <img src={url} alt={alt} className="plugin-slot__image-card-img" loading="lazy" />
        {alt && (
          <div className="plugin-slot__image-card-caption">{alt}</div>
        )}
      </div>
    );
  }

  return (
    <div className="plugin-slot plugin-slot--image">
      <img src={url} alt={alt} className="plugin-slot__image" loading="lazy" />
    </div>
  );
}

export function SlotFile({
  conversationId,
  slot,
}: {
  conversationId: string;
  slot: SlotRevision;
}) {
  const artifact = useSlotArtifact(slot.artifact_key, conversationId);
  const rawValue = artifact?.value as any;
  const url: string = rawValue?.url || (rawValue?.path ? resolveCoreAssetUrl(rawValue.path) : '');
  const name: string = rawValue?.filename ?? rawValue?.name ?? slot.artifact_key;
  const size: number | undefined = rawValue?.size;
  if (!url) {
    return (
      <div className="plugin-slot plugin-slot--file plugin-slot--empty">
        <span className="plugin-slot__placeholder">File pending…</span>
      </div>
    );
  }
  return (
    <div className="plugin-slot plugin-slot--file">
      <a
        href={url}
        download={name}
        target="_blank"
        rel="noopener noreferrer"
        className="plugin-slot__file-link"
        aria-label={`Download ${name}`}
      >
        <span className="plugin-slot__file-icon" aria-hidden="true">📄</span>
        <span className="plugin-slot__file-name">{name}</span>
        {size !== undefined && (
          <span className="plugin-slot__file-size">
            ({(size / 1024).toFixed(1)} KB)
          </span>
        )}
      </a>
    </div>
  );
}

/**
 * Normalize the content_type returned by the Python backend.
 * Python stores short forms: 'text', 'json', 'image', 'file', 'file_list'.
 * Map these to the canonical type name used for dispatch.
 */
function normalizeContentType(ct: string): 'image' | 'file' | 'text' {
  if (ct === 'image' || ct.startsWith('image/')) return 'image';
  if (ct === 'file' || ct === 'file_list' || ct === 'application/octet-stream' || ct.startsWith('application/')) return 'file';
  return 'text';
}

/** Shown when the artifact hasn't arrived yet (slot exists but no artifact in store). */
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

/**
 * SlotRenderer dispatches to the correct slot component based on content_type.
 * When the artifact hasn't loaded yet, renders a pending placeholder.
 * cardMode is forwarded to image slots for the horizontal card layout.
 * expectedType is used to pick the right pending placeholder before the artifact arrives.
 */
export function SlotRenderer({
  conversationId,
  slot,
  cardMode = false,
  expectedType,
}: {
  conversationId: string;
  slot: SlotRevision;
  cardMode?: boolean;
  expectedType?: 'image' | 'file' | 'text';
}) {
  const artifact = useSlotArtifact(slot.artifact_key, conversationId);

  if (!artifact) {
    return <SlotPending type={expectedType ?? 'text'} cardMode={cardMode} />;
  }

  const normalized = normalizeContentType(artifact.content_type);

  if (normalized === 'image') {
    return <SlotImage conversationId={conversationId} slot={slot} cardMode={cardMode} />;
  }
  if (normalized === 'file') {
    return <SlotFile conversationId={conversationId} slot={slot} />;
  }
  return <SlotText conversationId={conversationId} slot={slot} />;
}
