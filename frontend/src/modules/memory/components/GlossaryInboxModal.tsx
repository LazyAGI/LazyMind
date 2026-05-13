import {
  Alert,
  Button,
  Checkbox,
  Empty,
  Form,
  Input,
  Modal,
  Pagination,
  Select,
  Space,
  Spin,
  Tag,
  Tooltip,
} from "antd";
import type { TFunction } from "i18next";
import { useEffect, useState } from "react";
import type {
  GlossaryAsset,
  GlossaryChangeProposal,
  GlossaryMergeDraft,
  GlossaryConflictResolution,
  GlossaryConflictResolveMode,
  GlossarySource,
} from "../shared";

interface GlossaryInboxModalProps {
  t: TFunction;
  glossaryInboxOpen: boolean;
  setGlossaryInboxOpen: (open: boolean) => void;
  glossaryChangeProposals: GlossaryChangeProposal[];
  glossaryInboxLoading: boolean;
  glossaryInboxError: string;
  glossaryInboxSubmitting: "" | "accept" | "reject";
  refreshGlossaryConflicts: (options?: { showErrorToast?: boolean; silent?: boolean }) => void;
  glossarySourceColorMap: Record<GlossarySource, string>;
  glossarySourceLabelMap: Record<GlossarySource, string>;
  rejectGlossaryProposals: (proposals: GlossaryChangeProposal[]) => void;
  applyGlossaryProposals: (
    proposals: GlossaryChangeProposal[],
    resolutions?: Record<string, GlossaryConflictResolution>,
  ) => void;
}

type GlossaryInboxActionMode = GlossaryConflictResolveMode | "reject";
type GlossaryMergeStage = "select" | "edit" | "confirm";
type GlossaryCreateStage = "edit" | "confirm";

const mergeColorOptions = [
  { value: "red", label: "红色", color: "#d84a4a", textColor: "#b42318" },
  { value: "green", label: "绿色", color: "#9fd3ad", textColor: "#027a48" },
  { value: "blue", label: "蓝色", color: "#8bb7e8", textColor: "#175cd3" },
  { value: "yellow", label: "黄色", color: "#f4d06f", textColor: "#b54708" },
];
const MERGED_GROUP_OPTION_ID = "__merged_glossary_group__";
const MERGED_GROUP_OPTION_ID_PREFIX = `${MERGED_GROUP_OPTION_ID}:`;
const NEW_GROUP_OPTION_ID = "__new_glossary_group__";

const getDefaultResolution = (proposal: GlossaryChangeProposal): GlossaryConflictResolution => {
  const targetGroupIds = proposal.backendConflictGroupIds || [];
  const defaultTerm = proposal.after.term.trim();
  const defaultAliases = getUniqueTexts(proposal.after.aliases).filter(
    (alias) => alias !== defaultTerm,
  );
  return {
    mode: targetGroupIds.length ? "separate" : "create",
    selectedGroupIds: [],
    newGroupTerm: "",
    newGroupAliases: defaultAliases,
    newGroupContent: proposal.after.content,
  };
};

const getUniqueTexts = (items: string[]) =>
  [...new Set(items.map((item) => item.trim()).filter(Boolean))];

const getConflictWord = (proposal: GlossaryChangeProposal) =>
  proposal.backendConflictWord || proposal.after.term;

const getMergeColorMeta = (colorValue?: string) =>
  mergeColorOptions.find((item) => item.value === colorValue);

const buildMergeGroupsFromColors = (
  groups: GlossaryAsset[],
  colorMap: Record<string, string>,
): string[][] => {
  const grouped = new Map<string, string[]>();
  groups.forEach((group) => {
    const color = colorMap[group.id];
    if (!color) {
      return;
    }
    const ids = grouped.get(color) || [];
    ids.push(group.id);
    grouped.set(color, ids);
  });
  return Array.from(grouped.values()).filter((ids) => ids.length >= 2);
};

const buildMergeDraftFromGroupIds = (
  proposal: GlossaryChangeProposal,
  targetGroups: GlossaryAsset[],
  groupIds: string[],
): GlossaryMergeDraft => {
  const groups = targetGroups.filter((group) => groupIds.includes(group.id));
  const conflictWord = getConflictWord(proposal);
  const term = groups[0]?.term || proposal.after.term;
  const aliases = getUniqueTexts([
    conflictWord,
    proposal.after.term,
    ...proposal.after.aliases,
    ...groups.flatMap((group) => [group.term, ...group.aliases]),
  ]).filter((item) => item !== term);
  const content = getUniqueTexts([
    ...groups.map((group) => group.content),
    proposal.after.content,
  ]).join("\n\n");

  return {
    groupIds: [...groupIds],
    term,
    aliases,
    content,
  };
};

const syncMergeDraftsWithGroups = (
  proposal: GlossaryChangeProposal,
  targetGroups: GlossaryAsset[],
  currentDrafts: GlossaryMergeDraft[] = [],
  mergeGroups: string[][],
): GlossaryMergeDraft[] =>
  mergeGroups.map((groupIds) => {
    const current = currentDrafts.find(
      (draft) =>
        draft.groupIds.length === groupIds.length &&
        draft.groupIds.every((id) => groupIds.includes(id)),
    );
    if (current) {
      return { ...current, groupIds: [...groupIds] };
    }
    return buildMergeDraftFromGroupIds(proposal, targetGroups, groupIds);
  });

const buildCreateResolution = (
  proposal: GlossaryChangeProposal,
  resolution: GlossaryConflictResolution,
): GlossaryConflictResolution => {
  const conflictWord = getConflictWord(proposal);
  const normalizedTerm = resolution.newGroupTerm.trim();
  const shouldWriteConflictWordToNewGroup =
    !resolution.writeGroupIds || resolution.writeGroupIds.includes(NEW_GROUP_OPTION_ID);
  const aliases = getUniqueTexts([
    ...(shouldWriteConflictWordToNewGroup ? [conflictWord] : []),
    ...(resolution.newGroupAliases?.length
      ? resolution.newGroupAliases
      : proposal.after.aliases),
  ]).filter((alias) => alias !== normalizedTerm);

  return {
    ...resolution,
    mode: "create",
    selectedGroupIds: [],
    newGroupTerm: normalizedTerm,
    newGroupAliases: aliases,
    newGroupContent: (resolution.newGroupContent ?? proposal.after.content).trim(),
  };
};

const buildMergedDraft = (
  proposal: GlossaryChangeProposal,
  selectedGroups: GlossaryAsset[],
  resolution: GlossaryConflictResolution,
) => {
  const conflictWord = getConflictWord(proposal);
  const fallbackTerm = selectedGroups[0]?.term || proposal.after.term;
  const fallbackAliases = getUniqueTexts([
    conflictWord,
    proposal.after.term,
    ...proposal.after.aliases,
    ...selectedGroups.flatMap((group) => [group.term, ...group.aliases]),
  ]).filter((item) => item !== fallbackTerm);
  const fallbackContent = getUniqueTexts([
    ...selectedGroups.map((group) => group.content),
    proposal.after.content,
  ]).join("\n\n");

  return {
    term: (resolution.mergedGroupTerm || fallbackTerm).trim(),
    aliases: resolution.mergedGroupAliases?.length
      ? resolution.mergedGroupAliases
      : fallbackAliases,
    content: (resolution.mergedGroupContent ?? fallbackContent).trim(),
  };
};

const GlossaryGroupCards = ({
  groups,
  selectedGroupIds,
  disabled,
  lockedGroupIds = [],
  onChange,
  t,
  stacked = false,
}: {
  groups: GlossaryAsset[];
  selectedGroupIds: string[];
  disabled: boolean;
  lockedGroupIds?: string[];
  onChange: (groupIds: string[]) => void;
  t: TFunction;
  stacked?: boolean;
}) => (
  <div className={`memory-glossary-target-panel ${stacked ? "is-stacked" : ""}`}>
    <Checkbox.Group
      value={selectedGroupIds}
      disabled={disabled}
      onChange={(values) => onChange(values.map((value) => String(value)))}
    >
      <div className="memory-glossary-target-grid">
        {groups.map((group) => {
          const isSelected = selectedGroupIds.includes(group.id);
          const isLocked = lockedGroupIds.includes(group.id);
          return (
            <label
              key={group.id}
              className={`memory-glossary-target-card ${isSelected ? "is-selected" : ""}`}
            >
              <Checkbox value={group.id} disabled={disabled || isLocked} />
              <span className="memory-glossary-target-main">
                <strong>{group.term || t("admin.memoryGlossaryGroupUnassigned")}</strong>
                <span className="memory-tag-group memory-tag-group-scroll">
                  {group.aliases.length ? (
                    group.aliases.map((alias) => <Tag key={`${group.id}-${alias}`}>{alias}</Tag>)
                  ) : (
                    <span className="memory-content-preview">-</span>
                  )}
                </span>
              </span>
            </label>
          );
        })}
      </div>
    </Checkbox.Group>
  </div>
);

export default function GlossaryInboxModal(props: GlossaryInboxModalProps) {
  const {
    t,
    glossaryInboxOpen,
    setGlossaryInboxOpen,
    glossaryChangeProposals,
    glossaryInboxLoading,
    glossaryInboxError,
    glossaryInboxSubmitting,
    refreshGlossaryConflicts,
    glossarySourceColorMap,
    glossarySourceLabelMap,
    rejectGlossaryProposals,
    applyGlossaryProposals,
  } = props;
  const [resolutionMap, setResolutionMap] = useState<
    Record<string, GlossaryConflictResolution>
  >({});
  const [actionModeMap, setActionModeMap] = useState<
    Record<string, GlossaryInboxActionMode>
  >({});
  const [mergeStageMap, setMergeStageMap] = useState<Record<string, GlossaryMergeStage>>({});
  const [createStageMap, setCreateStageMap] = useState<Record<string, GlossaryCreateStage>>({});
  const [mergeColorMap, setMergeColorMap] = useState<Record<string, Record<string, string>>>({});
  const [mergeEditPageMap, setMergeEditPageMap] = useState<Record<string, number>>({});

  useEffect(() => {
    setResolutionMap((previous) => {
      const proposalIdSet = new Set(glossaryChangeProposals.map((proposal) => proposal.id));
      const next: Record<string, GlossaryConflictResolution> = {};

      glossaryChangeProposals.forEach((proposal) => {
        const resolution = previous[proposal.id] || getDefaultResolution(proposal);
        next[proposal.id] = resolution;
      });

      Object.keys(previous).forEach((proposalId) => {
        if (!proposalIdSet.has(proposalId)) {
          delete next[proposalId];
        }
      });

      return next;
    });
    setActionModeMap((previous) => {
      const next: Record<string, GlossaryInboxActionMode> = {};

      glossaryChangeProposals.forEach((proposal) => {
        next[proposal.id] = previous[proposal.id] || getDefaultResolution(proposal).mode;
      });

      return next;
    });
    setMergeColorMap(() => {
      const next: Record<string, Record<string, string>> = {};

      glossaryChangeProposals.forEach((proposal) => {
        next[proposal.id] = {};
      });

      return next;
    });
    setMergeStageMap((previous) => {
      const next: Record<string, GlossaryMergeStage> = {};

      glossaryChangeProposals.forEach((proposal) => {
        next[proposal.id] = previous[proposal.id] || "select";
      });

      return next;
    });
    setCreateStageMap((previous) => {
      const next: Record<string, GlossaryCreateStage> = {};

      glossaryChangeProposals.forEach((proposal) => {
        next[proposal.id] = previous[proposal.id] || "edit";
      });

      return next;
    });
    setMergeEditPageMap((previous) => {
      const next: Record<string, number> = {};
      glossaryChangeProposals.forEach((proposal) => {
        next[proposal.id] = previous[proposal.id] || 1;
      });
      return next;
    });
  }, [glossaryChangeProposals]);

  const isSubmitting = Boolean(glossaryInboxSubmitting);

  const updateResolution = (
    proposal: GlossaryChangeProposal,
    patch: Partial<GlossaryConflictResolution>,
  ) => {
    setResolutionMap((previous) => {
      const current = previous[proposal.id] || getDefaultResolution(proposal);
      return {
        ...previous,
        [proposal.id]: {
          ...current,
          ...patch,
        },
      };
    });
  };

  const setActionMode = (
    proposal: GlossaryChangeProposal,
    mode: GlossaryInboxActionMode,
  ) => {
    if (mode === "merge") {
      updateResolution(proposal, {
        mode,
        selectedGroupIds: [],
        mergeGroupIds: [],
        mergeGroups: [],
        mergeDrafts: [],
        writeGroupIds: undefined,
      });
      setMergeColorMap((previous) => ({
        ...previous,
        [proposal.id]: {},
      }));
      setMergeEditPageMap((previous) => ({
        ...previous,
        [proposal.id]: 1,
      }));
    } else if (mode !== "reject") {
      updateResolution(proposal, {
        mode,
        newGroupTerm: mode === "create" ? "" : undefined,
        writeGroupIds: mode === "create" ? undefined : undefined,
      });
    }
    setActionModeMap((previous) => ({
      ...previous,
      [proposal.id]: mode,
    }));
    if (mode === "merge") {
      setMergeStageMap((previous) => ({
        ...previous,
        [proposal.id]: "select",
      }));
    }
    if (mode === "create") {
      setCreateStageMap((previous) => ({
        ...previous,
        [proposal.id]: "edit",
      }));
    }
  };

  const submitProposalAction = (
    proposal: GlossaryChangeProposal,
    resolutionOverride?: GlossaryConflictResolution,
  ) => {
    const activeMode = actionModeMap[proposal.id] || getDefaultResolution(proposal).mode;
    const activeResolution =
      resolutionOverride || resolutionMap[proposal.id] || getDefaultResolution(proposal);

    if (activeMode === "reject") {
      rejectGlossaryProposals([proposal]);
      return;
    }

    if (activeMode === "merge") {
      const mergeGroupIds = activeResolution.mergeGroupIds?.length
        ? activeResolution.mergeGroupIds
        : activeResolution.selectedGroupIds;
      const selectedGroups = (proposal.backendConflictGroups || []).filter((group) =>
        mergeGroupIds.includes(group.id),
      );
      const mergedDraft = buildMergedDraft(proposal, selectedGroups, activeResolution);
      const isFullMerge = selectedGroups.length === (proposal.backendConflictGroups || []).length;
      const mergeGroups =
        activeResolution.mergeGroups?.filter((groupIds) => groupIds.length >= 2) ||
        (activeResolution.mergeGroupIds?.length ? [activeResolution.mergeGroupIds] : []);
      const mergedWriteGroupIds = mergeGroups.map(
        (groupIds, groupIndex) =>
          `${MERGED_GROUP_OPTION_ID_PREFIX}${groupIds[0] || `group-${groupIndex}`}`,
      );
      applyGlossaryProposals([proposal], {
        [proposal.id]: {
          ...activeResolution,
          mode: activeMode,
          selectedGroupIds: mergeGroupIds,
          mergeGroupIds,
          mergeGroups,
          writeGroupIds: isFullMerge
            ? undefined
            : activeResolution.writeGroupIds ?? mergedWriteGroupIds,
          mergedGroupTerm: mergedDraft.term,
          mergedGroupAliases: mergedDraft.aliases,
          mergedGroupContent: mergedDraft.content,
        },
      });
      return;
    }

    if (activeMode === "create") {
      const writeGroupIds = activeResolution.writeGroupIds ?? [NEW_GROUP_OPTION_ID];
      applyGlossaryProposals([proposal], {
        [proposal.id]: buildCreateResolution(proposal, {
          ...activeResolution,
          writeGroupIds,
        }),
      });
      return;
    }

    applyGlossaryProposals([proposal], {
      [proposal.id]: {
        ...activeResolution,
        mode: activeMode,
      },
    });
  };

  return (
    <Modal
      open={glossaryInboxOpen}
      title={t("admin.memoryGlossaryInboxTitle")}
      onCancel={() => setGlossaryInboxOpen(false)}
      width={980}
      footer={[
        <Button key="close" disabled={isSubmitting} onClick={() => setGlossaryInboxOpen(false)}>
          {t("common.close")}
        </Button>,
      ]}
    >
      {glossaryInboxError ? (
        <Alert
          type="error"
          showIcon
          className="memory-skill-share-alert"
          message={glossaryInboxError}
          action={
            <Button
              size="small"
              disabled={glossaryInboxLoading || isSubmitting}
              onClick={() => refreshGlossaryConflicts({ showErrorToast: true })}
            >
              {t("common.retry")}
            </Button>
          }
        />
      ) : null}

        {glossaryInboxLoading ? (
          <div className="memory-glossary-inbox-loading">
            <Spin />
            <span>{t("common.loading")}</span>
          </div>
        ) : glossaryChangeProposals.length ? (
          <div className="memory-glossary-inbox">
            <div className="memory-glossary-inbox-list">
              {glossaryChangeProposals.map((proposal) => {
                const isMergeProposal = Boolean(proposal.mergeFrom?.length);
                const targetGroups = proposal.backendConflictGroups || [];
                const proposalTypeText = isMergeProposal
                  ? t("admin.memoryGlossaryInboxTypeMerge")
                  : proposal.before
                    ? t("admin.memoryGlossaryInboxTypeUpdate")
                    : t("admin.memoryGlossaryInboxTypeAdd");

                return Array.from(
                  new Set(
                    Array.from(groupsByColor.values())
                      .filter((groupIds) => groupIds.length >= 2)
                      .flat(),
                  ),
                );
              };
              const isActionValid =
                actionMode === "reject" ||
                (actionMode === "create" &&
                  Boolean(createDraft.term) &&
                  !isCreateGroupInAliases &&
                  !isCreateContentSameAsTerm &&
                  (createStage !== "confirm" || createWriteGroupIds.length > 0)) ||
                (actionMode === "merge" &&
                  mergeStage === "confirm" &&
                  finalWriteGroupIds.length >= 1) ||
                (actionMode === "separate" && activeResolution.selectedGroupIds.length > 0);
              const confirmText =
                actionMode === "reject"
                  ? t("admin.memoryGlossaryInboxActionRejectTitle")
                  : actionMode === "merge"
                    ? t("admin.memoryGlossaryInboxMergeAndWrite")
                    : actionMode === "create"
                      ? t("admin.memoryGlossaryInboxCreateAndWrite")
                      : t("admin.memoryGlossaryInboxWriteSeparately");

              return (
                <section key={proposal.id} className="memory-glossary-inbox-card">
                  <div className="memory-glossary-inbox-card-head">
                    <div className="memory-glossary-inbox-title-block">
                      <span className="memory-glossary-inbox-note">{proposal.reason}</span>
                      <div className="memory-glossary-inbox-summary">
                        <strong>{index + 1}.</strong>
                        <GlossaryTermBubble asset={proposal.after} term={conflictWord} />
                        {targetGroups.length ? (
                          <>
                            <span>与</span>
                            {targetGroups.map((group, groupIndex) => (
                              <span
                                key={group.id}
                                className="memory-glossary-conflict-group"
                              >
                                <GlossaryTermBubble asset={group} />
                                {groupIndex < targetGroups.length - 1 ? "、" : null}
                              </span>
                            ))}
                            <span>产生了</span>
                            <span className="memory-glossary-inbox-conflict-text">冲突</span>
                            <span>，请处理</span>
                          </>
                        ) : (
                          <span>待确认写入方式</span>
                        )}
                      </div>
                    </div>
                    <Space size={8} wrap>
                      <Tag color="blue">{proposalTypeText}</Tag>
                      <Tag color={glossarySourceColorMap[proposal.after.source]}>
                        {glossarySourceLabelMap[proposal.after.source]}
                      </Tag>
                    </Space>
                  </div>

                  <div className="memory-glossary-inbox-card-grid">
                    <div className="memory-glossary-inbox-card-body">
                      <div className="memory-glossary-inbox-card-line">
                        <strong>{t("admin.memoryGlossaryAliases")}</strong>
                        <div className="memory-tag-group memory-tag-group-scroll">
                          {proposal.after.aliases.length ? (
                            proposal.after.aliases.map((alias: string) => (
                              <Tag key={`${proposal.id}-${alias}`}>{alias}</Tag>
                            ))
                          ) : (
                            <span className="memory-content-preview">-</span>
                          )}
                        </div>
                      </div>
                      {targetGroups.length ? (
                        <div className="memory-glossary-inbox-card-line">
                          <strong>{t("admin.memoryGlossaryInboxTargetGroups")}</strong>
                          <div className="memory-glossary-conflict-summary">
                            {targetGroups.map((group) => (
                              <Tag key={`${proposal.id}-${group.id}`}>
                                {group.term || group.id}
                              </Tag>
                            ))}
                          </div>
                        </div>
                      ) : null}
                    </div>
                    <div className="memory-glossary-inbox-card-actions">
                      <Button
                        size="small"
                        disabled={isSubmitting}
                        loading={glossaryInboxSubmitting === "reject"}
                        onClick={() => rejectGlossaryProposals([proposal])}
                      >
                        {t("admin.memoryGlossaryInboxReject")}
                      </Button>
                      <Button
                        size="small"
                        type="primary"
                        disabled={isSubmitting || !targetGroups.length}
                        onClick={() => openAction(proposal, "separate")}
                      >
                        {t("admin.memoryGlossaryInboxWriteSeparately")}
                      </Button>
                      <Button
                        size="small"
                        disabled={isSubmitting || targetGroups.length < 2}
                        onClick={() => openAction(proposal, "merge")}
                      >
                        {t("admin.memoryGlossaryInboxMergeAndWrite")}
                      </Button>
                      <Button
                        size="small"
                        disabled={isSubmitting}
                        onClick={() => openAction(proposal, "create")}
                      >
                        {t("admin.memoryGlossaryInboxCreateAndWrite")}
                      </Button>
                    </div>
                  </div>

                  {actionMode !== "merge" && actionMode !== "create" ? (
                    <div className="memory-glossary-inbox-card-actions">
                      <Button
                        className="memory-glossary-action-trigger"
                        disabled={!isActionValid || isSubmitting}
                        loading={
                          (actionMode === "reject" && glossaryInboxSubmitting === "reject") ||
                          (actionMode !== "reject" && glossaryInboxSubmitting === "accept")
                        }
                        onClick={() => submitProposalAction(proposal)}
                      >
                        {confirmText}
                      </Button>
                    </div>
                  ) : null}
                </section>
              );
            })}
          </div>
        </div>
      ) : (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={t("admin.memoryGlossaryInboxEmpty")}
        />
      )}
    </Modal>
  );
}
