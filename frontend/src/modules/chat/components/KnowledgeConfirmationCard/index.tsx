import { BookOutlined, CheckOutlined, FileTextOutlined, FolderOutlined, InfoCircleOutlined, RightOutlined, WarningOutlined } from "@ant-design/icons";
import { Button } from "antd";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { axiosInstance, BASE_URL } from "@/components/request";
import type { KnowledgeConfirmation, KnowledgeResourcePreview } from "@/modules/chat/types/knowledgeConfirmation";
import "./index.scss";

const actionKeys = {
  "knowledge.create": "library",
  "knowledge.folder.create": "folder",
  "knowledge.resource.move": "move",
  "knowledge.resource.delete": "delete",
} as const;

function ResourceIcon({ kind }: { kind?: NonNullable<KnowledgeResourcePreview["resource_ref"]>["kind"] }) {
  if (kind === "knowledge_base") return <BookOutlined aria-hidden="true" />;
  if (kind === "folder") return <FolderOutlined aria-hidden="true" />;
  return <FileTextOutlined aria-hidden="true" />;
}

interface DeleteScopeNode {
  key: number;
  resource: KnowledgeResourcePreview;
  children: DeleteScopeNode[];
}

function buildDeleteScope(resources: KnowledgeResourcePreview[]) {
  const nodes: DeleteScopeNode[] = resources.map((resource, key) => ({ key, resource, children: [] }));
  const directoryKey = (libraryId: string, path: string) => JSON.stringify([libraryId, path]);
  const directories = new Map<string, DeleteScopeNode | null>();
  for (const node of nodes) {
    const { resource_ref: ref, path } = node.resource;
    if (ref?.knowledge_base_id && ref.kind !== "document") {
      const key = directoryKey(ref.knowledge_base_id, path.replace(/\/$/, ""));
      directories.set(key, directories.has(key) ? null : node);
    }
  }

  const roots: DeleteScopeNode[] = [];
  for (const node of nodes) {
    const { resource_ref: ref, name, path } = node.resource;
    const suffix = `/${name}`;
    // Keep independent targets and ambiguous paths visible with their full paths.
    const parent = ref?.knowledge_base_id && ref.kind !== "knowledge_base" && name && path.endsWith(suffix)
      ? directories.get(directoryKey(ref.knowledge_base_id, path.slice(0, -suffix.length)))
      : undefined;
    (parent ? parent.children : roots).push(node);
  }
  return roots;
}

function DeleteScopeBranch({ node, nested }: { node: DeleteScopeNode; nested?: boolean }) {
  const { resource, children } = node;
  const hasChildren = children.length > 0;
  const row = <>
    {hasChildren ? <RightOutlined className="knowledge-confirmation__tree-arrow" aria-hidden="true" />
      : <span className="knowledge-confirmation__tree-arrow" aria-hidden="true" />}
    <ResourceIcon kind={resource.resource_ref?.kind} />
    <span>{nested ? resource.name : resource.path}</span>
  </>;
  return (
    <li>
      {hasChildren ? (
        <details>
          <summary className="knowledge-confirmation__tree-row">{row}</summary>
          <ul>{children.map(child => <DeleteScopeBranch key={child.key} node={child} nested />)}</ul>
        </details>
      ) : <div className="knowledge-confirmation__tree-row">{row}</div>}
    </li>
  );
}

export default function KnowledgeConfirmationCard({ card, readOnly }: { card: KnowledgeConfirmation; readOnly?: boolean }) {
  const { t } = useTranslation();
  const detailsId = useId();
  const deleteScopeId = `${detailsId}-scope`;
  const [expanded, setExpanded] = useState(false);
  const [decision, setDecision] = useState<"approved" | "rejected">();
  const [submitting, setSubmitting] = useState<"approved" | "rejected">();
  const submittingRef = useRef(false);
  const [failed, setFailed] = useState(false);
  const [expired, setExpired] = useState(() => !!card.expires_at && card.expires_at * 1000 <= Date.now());

  useEffect(() => {
    if (card.status !== "pending" || !card.expires_at || expired) return;
    const timer = window.setTimeout(() => setExpired(true), Math.max(0, card.expires_at * 1000 - Date.now()));
    return () => window.clearTimeout(timer);
  }, [card.status, card.expires_at, expired]);

  const { action, preview } = card;
  const status = card.status !== "pending" ? card.status : decision ?? (expired || readOnly ? "expired" : "pending");
  const pending = status === "pending";
  const showDetails = pending || expanded;
  const title = t(`chat.knowledgeConfirmation.title.${actionKeys[action]}`);
  const creatingLibrary = action === "knowledge.create";
  const creatingFolder = action === "knowledge.folder.create";
  const moving = action === "knowledge.resource.move";
  const deleting = action === "knowledge.resource.delete";
  const destination = preview.destination?.path;
  const atLibraryRoot = preview.destination?.resource_ref?.kind === "knowledge_base";
  const resources = preview.resources ?? [];
  const creation = preview.creation;
  const scene = creation?.scene;
  const level = creation?.processing_level;
  const folderCount = preview.folder_count ?? 0;
  const documentCount = preview.document_count ?? 0;
  const counts = [
    folderCount > 0 ? t("chat.knowledgeConfirmation.folders", { count: folderCount }) : "",
    documentCount > 0 ? t("chat.knowledgeConfirmation.documents", { count: documentCount }) : "",
  ].filter(Boolean).join(" · ");
  const summary = creatingLibrary ? creation?.name
    : moving ? destination
      : creatingFolder ? [destination, counts].filter(Boolean).join(" · ")
        : [resources.length === 1 && resources[0]?.name, counts].filter(Boolean).join(" · ");

  async function submit(next: "approved" | "rejected") {
    if (!pending || submittingRef.current) return;
    submittingRef.current = true;
    setSubmitting(next);
    setFailed(false);
    try {
      await axiosInstance.post(
        `${BASE_URL}/api/core/knowledge-management/confirmations/${encodeURIComponent(card.confirmation_id)}/decision`,
        { decision: next, arguments_digest: card.arguments_digest, snapshot: card.snapshot },
        { silentError: true } as import("axios").AxiosRequestConfig,
      );
      setDecision(next);
    } catch {
      setFailed(true);
    } finally {
      submittingRef.current = false;
      setSubmitting(undefined);
    }
  }

  return (
    <section className="knowledge-confirmation" aria-label={title}>
      <header className="knowledge-confirmation__header">
        <div className="knowledge-confirmation__heading">
          <strong>{title}</strong>
          {!showDetails && summary && <span className="knowledge-confirmation__summary-name">{summary}</span>}
        </div>
        <div className="knowledge-confirmation__status-area">
          <span className={`knowledge-confirmation__status is-${status}`} role="status">
            {status === "approved" && <CheckOutlined aria-hidden="true" />}
            {t(`chat.knowledgeConfirmation.status.${status}`)}
          </span>
          {!pending && (
            <Button type="text" size="small" className="knowledge-confirmation__disclosure"
              aria-expanded={expanded} aria-controls={detailsId} onClick={() => setExpanded(!expanded)}>
              {t(expanded ? "chat.askCardCollapse" : "chat.knowledgeConfirmation.details")}
              <RightOutlined rotate={expanded ? -90 : 90} aria-hidden="true" />
            </Button>
          )}
        </div>
      </header>
      <div id={detailsId} className="knowledge-confirmation__details" hidden={!showDetails}>
        {status === "superseded" && <p>{t("chat.knowledgeConfirmation.supersededDetails")}</p>}
        {creatingLibrary ? (
          <>
            <h3 className="knowledge-confirmation__name"><span><BookOutlined aria-hidden="true" /></span>{creation?.name}</h3>
            {creation?.description && <p>{creation.description}</p>}
            {!!creation?.tags?.length && (
              <ul className="knowledge-confirmation__tags" aria-label={t("knowledge.tags")}>
                {creation.tags.map(tag => <li key={tag}>{tag}</li>)}
              </ul>
            )}
            {(scene || level) && (
              <dl className="knowledge-confirmation__metadata">
                {scene && <div><dt>{t("chat.knowledgeCreationScene")}</dt><dd>{scene === "custom"
                  ? t("learning.customProfile")
                  : t(`learning.profile.${scene.replace(/_([a-z])/g, (_, letter: string) => letter.toUpperCase())}.name`, { defaultValue: scene })}</dd></div>}
                {level && <div><dt>{t("knowledge.processingLevel")}</dt><dd>{t(
                  `knowledge.processing${level[0].toUpperCase()}${level.slice(1)}`, { defaultValue: level },
                )}</dd></div>}
              </dl>
            )}
          </>
        ) : (
          <>
            <div className="knowledge-confirmation__scope">
              {deleting && <span id={deleteScopeId} className="knowledge-confirmation__scope-label">{t("chat.knowledgeConfirmation.deleteScope")}</span>}
              {destination && <span className="knowledge-confirmation__location">
                <span>{t("chat.knowledgeConfirmation.destination")}</span>
                <BookOutlined aria-hidden="true" />
                <strong>{creatingFolder && atLibraryRoot ? `${destination} / ${t("chat.knowledgeConfirmation.root")}` : destination}</strong>
              </span>}
              {counts && <span className="knowledge-confirmation__counts">{counts}</span>}
            </div>
            {moving && <div className="knowledge-confirmation__resource-label">
              {t("chat.knowledgeConfirmation.source")}
            </div>}
            {deleting ? showDetails && (
              <ul className="knowledge-confirmation__delete-tree" aria-labelledby={deleteScopeId}>
                {buildDeleteScope(resources).map(node => <DeleteScopeBranch key={node.key} node={node} />)}
              </ul>
            ) : <ul className="knowledge-confirmation__resources">
              {creatingFolder ? creation?.paths?.map(path => <li key={path}>
                <FolderOutlined aria-hidden="true" />
                <span>{destination && path.startsWith(`${destination}/`) ? path.slice(destination.length + 1) : path}</span>
              </li>) : resources.map(resource => <li key={resource.path}>
                <ResourceIcon kind={resource.resource_ref?.kind} />
                <span>{resource.path}</span>
              </li>)}
            </ul>}
            {(deleting || moving) && (
              <p className={`knowledge-confirmation__impact${deleting ? " is-danger" : ""}`}>
                {deleting ? <WarningOutlined aria-hidden="true" /> : <InfoCircleOutlined aria-hidden="true" />}
                {t(deleting ? "chat.knowledgeConfirmation.deleteImpact" : "chat.knowledgeConfirmation.moveImpact")}
              </p>
            )}
          </>
        )}
        {pending && failed && <p className="knowledge-confirmation__error" role="alert">{t("chat.knowledgeConfirmationFailed")}</p>}
      </div>
      {pending && (
        <footer className="knowledge-confirmation__actions">
          <Button disabled={!!submitting} loading={submitting === "rejected"} onClick={() => void submit("rejected")}>
            {t("common.cancel")}
          </Button>
          <Button type="primary" danger={deleting} disabled={!!submitting} loading={submitting === "approved"}
            onClick={() => void submit("approved")}>
            {t(`chat.knowledgeConfirmation.confirm.${deleting ? "delete" : moving ? "move" : "create"}`)}
          </Button>
        </footer>
      )}
    </section>
  );
}
