import { useTaskCenterStore, type TaskArtifact } from "@/modules/chat/store/taskCenter";
import type { SlotRevision } from "@/modules/chat/store/pluginPanel";

function resolveArtifactValue(
  artifact_key: string,
  conversationId: string,
): TaskArtifact | undefined {
  const tasks = useTaskCenterStore.getState().getTasks(conversationId);
  for (const task of tasks) {
    const match = task.artifacts.find((a) => a.artifact_key === artifact_key);
    if (match) return match;
  }
  return undefined;
}

export function SlotText({
  conversationId,
  slot,
}: {
  conversationId: string;
  slot: SlotRevision;
}) {
  const artifact = resolveArtifactValue(slot.artifact_key, conversationId);
  const text = (artifact?.value as any)?.text ?? slot.artifact_key;
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
  const artifact = resolveArtifactValue(slot.artifact_key, conversationId);
  const url: string = (artifact?.value as any)?.url ?? "";
  const alt: string = (artifact?.value as any)?.alt ?? "";

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
  const artifact = resolveArtifactValue(slot.artifact_key, conversationId);
  const url: string = (artifact?.value as any)?.url ?? "";
  const name: string = (artifact?.value as any)?.name ?? slot.artifact_key;
  const size: number | undefined = (artifact?.value as any)?.size;
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
 * SlotRenderer dispatches to the correct slot component based on content_type.
 * cardMode is forwarded to image slots for the horizontal card layout.
 */
export function SlotRenderer({
  conversationId,
  slot,
  cardMode = false,
}: {
  conversationId: string;
  slot: SlotRevision;
  cardMode?: boolean;
}) {
  const artifact = resolveArtifactValue(slot.artifact_key, conversationId);
  const contentType: string = artifact?.content_type ?? "text/plain";

  if (contentType.startsWith("image/")) {
    return <SlotImage conversationId={conversationId} slot={slot} cardMode={cardMode} />;
  }
  if (contentType === "application/octet-stream" || contentType.startsWith("application/")) {
    return <SlotFile conversationId={conversationId} slot={slot} />;
  }
  return <SlotText conversationId={conversationId} slot={slot} />;
}
