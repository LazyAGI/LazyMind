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
  type ProfileDocument,
  type ProfilePatch,
  type SoulDocument,
  type SoulPatch,
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

const buildSoulSetPatch = (
  path: string,
  value: IdentityFieldValue,
): SoulPatch => ({
  operations: [{ op: "set", path, value: stringFieldValue(value) }],
});

const buildProfileScalarPatch = (
  path: string,
  value: IdentityFieldValue,
): ProfilePatch => {
  const next = stringFieldValue(value).trim();
  return next
    ? { operations: [{ op: "set", path, value: next }] }
    : { operations: [{ op: "clear", path }] };
};

const buildProfileListPatch = (
  op: "add" | "remove",
  path: string,
  value: IdentityFieldValue,
): ProfilePatch => ({
  operations: [{ op, path, value: stringFieldValue(value).trim() }],
});

interface IdentityDocumentCardProps<TDocument, TPatch> {
  kind: MemoryKind;
  load: () => Promise<CurrentMemorySnapshot<TDocument>>;
  save: (patch: TPatch) => Promise<CurrentMemorySnapshot<TDocument>>;
  fieldsFor: (document: TDocument) => IdentityField<TPatch>[];
  summaryFor: (document: TDocument) => IdentityCardSummary;
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

function IdentityDocumentCard<TDocument, TPatch>({
  kind,
  load,
  save,
  fieldsFor,
  summaryFor,
}: IdentityDocumentCardProps<TDocument, TPatch>) {
  const { i18n, t } = useTranslation();
  const [snapshot, setSnapshot] =
    useState<CurrentMemorySnapshot<TDocument> | null>(null);
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
    () => (snapshot ? fieldsFor(snapshot.document) : []),
    [fieldsFor, snapshot],
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

  const { mission, name, role, tags } = summaryFor(snapshot.document);
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
            {tags.filter(Boolean).slice(0, 4).map((tag) => (
              <Tag bordered={false} key={tag}>
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
            {fields.map((field) => {
              const editing = editingField?.path === field.path;
              return (
                <div
                  className={`memory-identity-field ${editing ? "is-editing" : ""}`}
                  key={field.path}
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

  const soulFieldsFor = useCallback(
    (document: SoulDocument): IdentityField<SoulPatch>[] => [
      {
        path: "identity.name",
        label: t("admin.memorySoulIdentityName"),
        value: document.identity.name,
        valueType: "required-string",
        buildPatch: (value) => buildSoulSetPatch("identity.name", value),
      },
      {
        path: "identity.role",
        label: t("admin.memorySoulIdentityRole"),
        value: document.identity.role,
        valueType: "required-string",
        buildPatch: (value) => buildSoulSetPatch("identity.role", value),
      },
      {
        path: "identity.description",
        label: t("admin.memorySoulIdentityDescription"),
        value: document.identity.description,
        valueType: "required-string",
        buildPatch: (value) =>
          buildSoulSetPatch("identity.description", value),
      },
      {
        path: "mission.primary_goal",
        label: t("admin.memorySoulPrimaryGoal"),
        value: document.mission.primary_goal,
        valueType: "required-string",
        buildPatch: (value) =>
          buildSoulSetPatch("mission.primary_goal", value),
      },
      {
        path: "mission.success_definition",
        label: t("admin.memorySoulSuccessDefinition"),
        value: document.mission.success_definition,
        valueType: "required-string",
        buildPatch: (value) =>
          buildSoulSetPatch("mission.success_definition", value),
      },
      ...(
        [
          "default_relationship_mode",
          "default_tone",
          "default_initiative_level",
          "default_challenge_level",
          "default_decision_mode",
        ] as const
      ).map((key) => ({
        path: `interaction.${key}`,
        label: t(`admin.memorySoulInteraction_${key}`),
        value: document.interaction[key],
        valueType: "required-string" as const,
        buildPatch: (value: IdentityFieldValue) =>
          buildSoulSetPatch(`interaction.${key}`, value),
      })),
      ...(["uncertainty_style", "verification_mode"] as const).map((key) => ({
        path: `epistemic.${key}`,
        label: t(`admin.memorySoulEpistemic_${key}`),
        value: document.epistemic[key],
        valueType: "required-string" as const,
        buildPatch: (value: IdentityFieldValue) =>
          buildSoulSetPatch(`epistemic.${key}`, value),
      })),
    ],
    [t],
  );

  const profileFieldsFor = useCallback(
    (document: ProfileDocument): IdentityField<ProfilePatch>[] => [
      ...(
        [
          ["preferred_name", "nullable-string"],
          ["aliases", "string-list"],
        ] as const
      ).map(([key, valueType]) => ({
        path: `identity.${key}`,
        label: t(`admin.memoryProfileIdentity_${key}`),
        value: document.identity[key],
        valueType,
        buildPatch: (value: IdentityFieldValue) =>
          valueType === "string-list"
            ? buildProfileListPatch("add", `identity.${key}`, value)
            : buildProfileScalarPatch(`identity.${key}`, value),
        ...(valueType === "string-list"
          ? {
              buildRemovePatch: (value: string) =>
                buildProfileListPatch("remove", `identity.${key}`, value),
            }
          : {}),
      })),
      ...(
        [
          ["languages", "string-list"],
          ["residence", "nullable-string"],
        ] as const
      ).map(([key, valueType]) => ({
        path: `locale.${key}`,
        label: t(`admin.memoryProfileLocale_${key}`),
        value: document.locale[key],
        valueType,
        buildPatch: (value: IdentityFieldValue) =>
          valueType === "string-list"
            ? buildProfileListPatch("add", `locale.${key}`, value)
            : buildProfileScalarPatch(`locale.${key}`, value),
        ...(valueType === "string-list"
          ? {
              buildRemovePatch: (value: string) =>
                buildProfileListPatch("remove", `locale.${key}`, value),
            }
          : {}),
      })),
      ...(
        [
          ["occupations", "string-list"],
          ["organizations", "string-list"],
          ["industries", "string-list"],
          ["expertise_domains", "string-list"],
        ] as const
      ).map(([key, valueType]) => ({
        path: `professional.${key}`,
        label: t(`admin.memoryProfileProfessional_${key}`),
        value: document.professional[key],
        valueType,
        buildPatch: (value: IdentityFieldValue) =>
          buildProfileListPatch("add", `professional.${key}`, value),
        buildRemovePatch: (value: string) =>
          buildProfileListPatch("remove", `professional.${key}`, value),
      })),
    ],
    [t],
  );

  const soulSummaryFor = useCallback(
    (document: SoulDocument): IdentityCardSummary => ({
      name: document.identity.name,
      role: document.identity.role,
      mission: document.mission.primary_goal,
      tags: [
        document.interaction.default_relationship_mode,
        document.interaction.default_initiative_level,
        document.interaction.default_challenge_level,
        document.epistemic.uncertainty_style,
      ],
    }),
    [],
  );

  const profileSummaryFor = useCallback(
    (document: ProfileDocument): IdentityCardSummary => ({
      name:
        document.identity.preferred_name ||
        t("admin.memoryCurrentProfileFallbackName"),
      role:
        [
          ...document.professional.occupations,
          ...document.professional.industries,
        ]
          .filter(Boolean)
          .join(" · ") || t("admin.memoryCurrentProfileFallbackRole"),
      mission:
        [
          document.locale.languages.join(" · "),
          document.locale.residence,
        ]
          .filter(Boolean)
          .join(" · ") || t("admin.memoryCurrentProfileNoLocale"),
      tags: [
        ...document.professional.expertise_domains,
        ...document.professional.organizations,
        ...document.identity.aliases,
      ],
    }),
    [t],
  );

  return (
    <section
      className="memory-current-identity-section"
      aria-label={t("admin.memoryCurrentIdentityTitle")}
    >
      <div className="memory-identity-grid">
        <IdentityDocumentCard
          fieldsFor={soulFieldsFor}
          kind="soul"
          load={getSoulMemory}
          save={patchSoulMemory}
          summaryFor={soulSummaryFor}
        />
        <IdentityDocumentCard
          fieldsFor={profileFieldsFor}
          kind="profile"
          load={getProfileMemory}
          save={patchProfileMemory}
          summaryFor={profileSummaryFor}
        />
      </div>
    </section>
  );
}
