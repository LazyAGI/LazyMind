import { EditOutlined, PlusOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Empty,
  Input,
  message,
  Modal,
  Popconfirm,
  Skeleton,
  Tag,
  Upload,
} from "antd";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  IDENTITY_AVATAR_ACCEPT,
  IdentityAvatar,
  IdentityAvatarValidationError,
  useIdentityAvatarStore,
  validateIdentityAvatarFile,
  type IdentityAvatarKind,
} from "@/modules/identityAvatar";
import {
  getProfileMemory,
  getSoulMemory,
  patchProfileMemory,
  patchSoulMemory,
  type CurrentMemorySnapshot,
  type MemoryDocument,
  type MemoryPatch,
} from "../../currentMemoryApi";
import {
  isCurrentMemoryResourceNotFound,
} from "../../currentMemoryViewModel";
import {
  type IdentityField,
  type IdentityFieldValue,
  useIdentityFieldEditor,
} from "./useIdentityFieldEditor";

type MemoryKind = "soul" | "profile";

interface IdentityCardSummary {
  name: string;
  role: string;
  mission: string;
  tags: string[];
}

const fieldDisplayValue = (
  value: IdentityFieldValue,
  emptyText: string,
) => {
  if (Array.isArray(value)) {
    return value.length ? value.join(" · ") : emptyText;
  }
  return value?.trim() || emptyText;
};

const stringFieldValue = (value: IdentityFieldValue) =>
  typeof value === "string" ? value : "";

const buildScalarPatch = (
  path: string,
  value: IdentityFieldValue,
): MemoryPatch => {
  const next = stringFieldValue(value).trim();
  return next
    ? { operations: [{ op: "set", path, value: next }] }
    : { operations: [{ op: "clear", path }] };
};

const buildListPatch = (
  op: "add" | "remove",
  path: string,
  value: IdentityFieldValue,
): MemoryPatch => ({
  operations: [{ op, path, value: stringFieldValue(value).trim() }],
});

const buildClearPatch = (path: string): MemoryPatch => ({
  operations: [{ op: "clear", path }],
});

type Presentation = CurrentMemorySnapshot<MemoryDocument>["presentation"];

const localizedText = (
  labels: Record<string, string> | undefined,
  locale: string,
  fallback: string,
) => {
  const normalizedLocale = locale.replace("_", "-");
  return (
    labels?.[normalizedLocale] ||
    labels?.["en-US"] ||
    fallback
  );
};

const documentValueAt = (
  document: MemoryDocument,
  path: string,
): IdentityFieldValue | undefined => {
  let current: unknown = document;
  for (const part of path.split(".")) {
    if (
      !current ||
      Array.isArray(current) ||
      typeof current !== "object" ||
      !(part in current)
    ) {
      return undefined;
    }
    current = (current as Record<string, unknown>)[part];
  }
  if (
    current === null ||
    typeof current === "string" ||
    (Array.isArray(current) &&
      current.every((item) => typeof item === "string"))
  ) {
    return current as IdentityFieldValue;
  }
  return undefined;
};

const valuesForSummaryRole = (
  document: MemoryDocument,
  presentation: Presentation,
  role: "title" | "subtitle" | "description" | "tag",
) =>
  presentation.sections.flatMap((section) =>
    section.fields
      .filter((field) => field.summary_role === role)
      .flatMap((field) => {
        const value = documentValueAt(document, field.path);
        return Array.isArray(value)
          ? value.filter(Boolean)
          : value?.trim()
            ? [value.trim()]
            : [];
      }),
  );

const fieldsFromPresentation = (
  snapshot: CurrentMemorySnapshot<MemoryDocument>,
  locale: string,
): IdentityField<MemoryPatch>[] =>
  snapshot.presentation.sections.flatMap((section) =>
    section.fields.flatMap((field) => {
      const value = documentValueAt(snapshot.document, field.path);
      if (value === undefined) {
        return [];
      }
      const valueType = Array.isArray(value) ? "string-list" : "string";
      return [{
        path: field.path,
        sectionPath: section.path,
        sectionLabel: localizedText(
          section.labels,
          locale,
          section.path.split(".").at(-1) || section.path,
        ),
        label: localizedText(
          field.labels,
          locale,
          field.path.split(".").at(-1) || field.path,
        ),
        value,
        valueType,
        buildPatch: (nextValue: IdentityFieldValue) =>
          valueType === "string-list"
            ? buildListPatch("add", field.path, nextValue)
            : buildScalarPatch(field.path, nextValue),
        ...(valueType === "string-list"
          ? {
              buildRemovePatch: (item: string) =>
                buildListPatch("remove", field.path, item),
            }
          : {}),
        buildClearPatch: () => buildClearPatch(field.path),
      }];
    }),
  );

const summaryFromPresentation = (
  snapshot: CurrentMemorySnapshot<MemoryDocument>,
  locale: string,
): IdentityCardSummary => {
  const { document, presentation } = snapshot;
  const fallback = (role: "title" | "subtitle" | "description") =>
    localizedText(presentation.fallbacks[role], locale, "");
  const title = valuesForSummaryRole(document, presentation, "title");
  const subtitle = valuesForSummaryRole(document, presentation, "subtitle");
  const description = valuesForSummaryRole(
    document,
    presentation,
    "description",
  );
  return {
    name: title[0] || fallback("title"),
    role: subtitle.join(" · ") || fallback("subtitle"),
    mission: description.join(" · ") || fallback("description"),
    tags: valuesForSummaryRole(document, presentation, "tag"),
  };
};

interface IdentityDocumentCardProps {
  kind: MemoryKind;
  load: () => Promise<CurrentMemorySnapshot<MemoryDocument>>;
  save: (patch: MemoryPatch) => Promise<CurrentMemorySnapshot<MemoryDocument>>;
}

function IdentityAvatarEditor({
  kind,
  size = 58,
}: {
  kind: IdentityAvatarKind;
  size?: number;
}) {
  const { t } = useTranslation();
  const entry = useIdentityAvatarStore((state) => state.avatars[kind]);
  const load = useIdentityAvatarStore((state) => state.load);
  const remove = useIdentityAvatarStore((state) => state.remove);
  const upload = useIdentityAvatarStore((state) => state.upload);
  const [errorMessage, setErrorMessage] = useState("");
  const busy = entry.status === "loading";
  const hasCustomAvatar = Boolean(entry.url);

  const handleUpload = async (file: File) => {
    setErrorMessage("");
    try {
      validateIdentityAvatarFile(file);
      await upload(kind, file);
      message.success(t("identityAvatar.uploadSuccess"));
    } catch (error) {
      setErrorMessage(
        error instanceof IdentityAvatarValidationError
          ? t(`identityAvatar.validation.${error.reason}`)
          : getLocalizedErrorMessage(error),
      );
    }
  };

  const handleRemove = async () => {
    setErrorMessage("");
    try {
      await remove(kind);
      message.success(t("identityAvatar.restoreSuccess"));
    } catch (error) {
      setErrorMessage(getLocalizedErrorMessage(error));
    }
  };

  return (
    <div
      className={`memory-identity-avatar-editor${busy ? " is-loading" : ""}`}
    >
      <Upload
        accept={IDENTITY_AVATAR_ACCEPT}
        beforeUpload={(file) => {
          void handleUpload(file);
          return Upload.LIST_IGNORE;
        }}
        disabled={busy}
        maxCount={1}
        multiple={false}
        showUploadList={false}
      >
        <button
          aria-label={t("identityAvatar.change")}
          className="memory-identity-avatar-button"
          disabled={busy}
          type="button"
        >
          <IdentityAvatar
            className="memory-identity-avatar"
            kind={kind}
            size={size}
          />
          <span className="memory-identity-avatar-overlay">
            {busy ? t("common.loading") : t("identityAvatar.change")}
          </span>
        </button>
      </Upload>

      {hasCustomAvatar ? (
        <Popconfirm
          cancelText={t("common.cancel")}
          description={t("identityAvatar.restoreConfirm")}
          okText={t("identityAvatar.restore")}
          title={t("identityAvatar.restore")}
          onConfirm={() => void handleRemove()}
        >
          <Button
            className="memory-identity-avatar-restore"
            disabled={busy}
            size="small"
            type="link"
          >
            {t("identityAvatar.restore")}
          </Button>
        </Popconfirm>
      ) : null}

      {entry.status === "error" || errorMessage ? (
        <Alert
          action={
            entry.status === "error" ? (
              <Button
                size="small"
                onClick={() => {
                  setErrorMessage("");
                  void load(kind, true);
                }}
              >
                {t("common.retry")}
              </Button>
            ) : null
          }
          className="memory-identity-avatar-error"
          message={errorMessage || t("identityAvatar.loadFailed")}
          showIcon
          type="error"
        />
      ) : null}
    </div>
  );
}

function IdentityDocumentCard({
  kind,
  load,
  save,
}: IdentityDocumentCardProps) {
  const { i18n, t } = useTranslation();
  const [snapshot, setSnapshot] =
    useState<CurrentMemorySnapshot<MemoryDocument> | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [detailOpen, setDetailOpen] = useState(false);
  const {
    beginEdit,
    cancelEdit,
    conflict,
    draftValue,
    editingField,
    reloadConflictSnapshot,
    retrySave,
    saveError,
    saveField,
    savePatch,
    saving,
    updateDraftValue,
  } = useIdentityFieldEditor({
    kind,
    load,
    save,
    setSnapshot,
  });

  const refresh = useCallback(async () => {
    setLoading(true);
    setLoadError("");
    try {
      setSnapshot(await load());
    } catch (error) {
      if (isCurrentMemoryResourceNotFound(error)) {
        setSnapshot(null);
      } else {
        console.error(`Load ${kind} memory failed:`, error);
        setLoadError(getLocalizedErrorMessage(error));
      }
    } finally {
      setLoading(false);
    }
  }, [kind, load]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const fields = useMemo(
    () =>
      snapshot
        ? fieldsFromPresentation(
            snapshot,
            i18n.resolvedLanguage || i18n.language,
          )
        : [],
    [i18n.language, i18n.resolvedLanguage, snapshot],
  );
  const displayUpdatedAt = useMemo(() => {
    if (!snapshot?.updatedAt) {
      return t("admin.memoryCurrentUnknownTime");
    }
    const date = new Date(snapshot.updatedAt);
    if (Number.isNaN(date.getTime())) {
      return snapshot.updatedAt;
    }
    return new Intl.DateTimeFormat(
      i18n.resolvedLanguage || i18n.language,
      {
        dateStyle: "medium",
        timeStyle: "short",
      },
    ).format(date);
  }, [i18n.language, i18n.resolvedLanguage, snapshot?.updatedAt, t]);

  const isSoul = kind === "soul";
  const title = t(
    isSoul
      ? "admin.memoryCurrentSoulTitle"
      : "admin.memoryCurrentProfileTitle",
  );
  const description = t(
    isSoul
      ? "admin.memoryCurrentSoulDescription"
      : "admin.memoryCurrentProfileDescription",
  );

  if (loading && !snapshot) {
    return (
      <article
        className={`memory-identity-card is-${kind} is-loading`}
        aria-busy="true"
      >
        <Skeleton active paragraph={{ rows: 4 }} />
      </article>
    );
  }

  if (loadError && !snapshot) {
    return (
      <article className={`memory-identity-card is-${kind} is-error`}>
        <Alert
          action={
            <Button size="small" onClick={() => void refresh()}>
              {t("common.retry")}
            </Button>
          }
          description={loadError}
          message={t("admin.memoryCurrentLoadFailed", { type: title })}
          showIcon
          type="error"
        />
      </article>
    );
  }

  if (!snapshot) {
    return (
      <article className={`memory-identity-card is-${kind} is-empty`}>
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={t("admin.memoryCurrentEmpty", { type: title })}
        />
      </article>
    );
  }

  const { mission, name, role, tags } = summaryFromPresentation(
    snapshot,
    i18n.resolvedLanguage || i18n.language,
  );
  const configuredCount = fields.filter((field) =>
    Array.isArray(field.value)
      ? field.value.length > 0
      : Boolean(field.value?.trim()),
  ).length;

  return (
    <>
      <article className={`memory-identity-card is-${kind}`}>
        <span className="memory-identity-watermark is-large" />
        <span className="memory-identity-watermark is-small" />
        <button
          type="button"
          className="memory-identity-card-button"
          aria-label={t("admin.memoryCurrentViewDetail", { type: title })}
          onClick={() => setDetailOpen(true)}
        >
          <span className="memory-identity-eyebrow">
            {t(
              isSoul
                ? "admin.memoryCurrentSoulEyebrow"
                : "admin.memoryCurrentProfileEyebrow",
            )}
          </span>
          <span className="memory-identity-main">
            <IdentityAvatar
              className="memory-identity-avatar"
              kind={kind}
              size={58}
            />
            <span>
              <strong>{name}</strong>
              <small>{role}</small>
            </span>
          </span>
          <span className="memory-identity-mission">{mission}</span>
          <span className="memory-identity-tags">
            {tags.filter(Boolean).slice(0, 4).map((tag, index) => (
              <Tag bordered={false} key={`${tag}-${index}`}>
                {tag}
              </Tag>
            ))}
          </span>
          <span className="memory-identity-footer">
            {isSoul
              ? t("admin.memoryCurrentViewAllFields")
              : t("admin.memoryCurrentConfiguredFields", {
                  configured: configuredCount,
                  total: fields.length,
                })}
            <span>→</span>
          </span>
        </button>
      </article>

      <Modal
        destroyOnHidden
        footer={null}
        open={detailOpen}
        title={t("admin.memoryCurrentDetailTitle", { type: title })}
        width={760}
        onCancel={() => {
          if (saving) {
            return;
          }
          setDetailOpen(false);
          cancelEdit();
        }}
      >
        <div className={`memory-identity-detail is-${kind}`}>
          <div className="memory-identity-detail-hero">
            <IdentityAvatarEditor kind={kind} />
            <div>
              <h4>{name}</h4>
              <p>{description}</p>
              <small>
                {t("admin.memoryCurrentUpdatedAt", {
                  time: displayUpdatedAt,
                })}
              </small>
            </div>
          </div>

          {conflict ? (
            <Alert
              action={
                <div className="memory-current-conflict-actions">
                  <Button
                    disabled={saving}
                    size="small"
                    onClick={() => void reloadConflictSnapshot()}
                  >
                    {t("admin.memoryCurrentLoadLatest")}
                  </Button>
                  <Button
                    disabled={saving}
                    size="small"
                    type="primary"
                    onClick={() => void retrySave()}
                  >
                    {t("admin.memoryCurrentRetrySave")}
                  </Button>
                </div>
              }
              description={t("admin.memoryCurrentConflictDescription")}
              message={t("admin.memoryCurrentConflictTitle")}
              showIcon
              type="warning"
            />
          ) : null}

          <div className="memory-identity-fields">
            {fields.map((field, fieldIndex) => {
              const editing = editingField?.path === field.path;
              return (
                <div key={field.path}>
                  {fieldIndex === 0 ||
                  fields[fieldIndex - 1].sectionPath !== field.sectionPath ? (
                    <h5 className="memory-identity-section-title">
                      {field.sectionLabel}
                    </h5>
                  ) : null}
                  <div
                    className={`memory-identity-field ${editing ? "is-editing" : ""}`}
                  >
                  <div className="memory-identity-field-row">
                    <span className="memory-identity-field-key">
                      {field.label}
                    </span>
                    <span className="memory-identity-field-value">
                      {Array.isArray(field.value) ? (
                        field.value.length ? (
                          field.value.map((item) => (
                            <Tag
                              closable={Boolean(field.buildRemovePatch)}
                              key={item}
                              onClose={(event) => {
                                event.preventDefault();
                                if (field.buildRemovePatch) {
                                  void savePatch(
                                    field.buildRemovePatch(item),
                                    false,
                                  );
                                }
                              }}
                            >
                              {item}
                            </Tag>
                          ))
                        ) : (
                          t("admin.memoryCurrentNotConfigured")
                        )
                      ) : (
                        fieldDisplayValue(
                          field.value,
                          t("admin.memoryCurrentNotConfigured"),
                        )
                      )}
                    </span>
                    <Button
                      aria-label={t("admin.memoryCurrentEditField", {
                        field: field.label,
                      })}
                      disabled={saving}
                      icon={
                        field.valueType === "string-list" ? (
                          <PlusOutlined />
                        ) : (
                          <EditOutlined />
                        )
                      }
                      size="small"
                      type="text"
                      onClick={() => beginEdit(field)}
                    />
                    {(Array.isArray(field.value)
                      ? field.value.length > 0
                      : Boolean(field.value?.trim())) ? (
                      <Button
                        disabled={saving}
                        size="small"
                        type="text"
                        onClick={() =>
                          void savePatch(field.buildClearPatch(), false)
                        }
                      >
                        {t("admin.memoryCurrentClearField")}
                      </Button>
                    ) : null}
                  </div>

                  {editing ? (
                    <div className="memory-identity-field-editor">
                      {field.valueType === "string-list" ? (
                        <Input
                          autoFocus
                          disabled={saving}
                          value={
                            typeof draftValue === "string" ? draftValue : ""
                          }
                          onChange={(event) => {
                            updateDraftValue(event.target.value);
                          }}
                          onPressEnter={(event) => {
                            if (!event.nativeEvent.isComposing) {
                              event.preventDefault();
                              void saveField();
                            }
                          }}
                        />
                      ) : (
                        <Input.TextArea
                          autoFocus
                          autoSize={{ minRows: 2, maxRows: 6 }}
                          disabled={saving}
                          value={
                            typeof draftValue === "string"
                              ? draftValue
                              : ""
                          }
                          onChange={(event) => {
                            updateDraftValue(event.target.value);
                          }}
                          onPressEnter={(event) => {
                            if (!event.shiftKey) {
                              event.preventDefault();
                              void saveField();
                            }
                          }}
                        />
                      )}
                      {saveError ? (
                        <Alert
                          message={saveError}
                          showIcon
                          type="error"
                        />
                      ) : null}
                      <div className="memory-identity-field-actions">
                        <Button disabled={saving} onClick={cancelEdit}>
                          {t("common.cancel")}
                        </Button>
                        <Button
                          loading={saving}
                          type="primary"
                          onClick={() => void saveField()}
                        >
                          {t("admin.memoryCurrentSaveField")}
                        </Button>
                      </div>
                    </div>
                  ) : null}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </Modal>
    </>
  );
}

export default function CurrentMemoryIdentitySection() {
  const { t } = useTranslation();

  return (
    <section
      className="memory-current-identity-section"
      aria-label={t("admin.memoryCurrentIdentityTitle")}
    >
      <div className="memory-identity-grid">
        <IdentityDocumentCard
          kind="soul"
          load={getSoulMemory}
          save={patchSoulMemory}
        />
        <IdentityDocumentCard
          kind="profile"
          load={getProfileMemory}
          save={patchProfileMemory}
        />
      </div>
    </section>
  );
}
