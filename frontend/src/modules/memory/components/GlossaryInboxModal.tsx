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

const getActionModeLabel = (mode: GlossaryInboxActionMode, conflictWord: string) => {
  if (mode === "reject") {
    return "放弃该词";
  }
  if (mode === "merge") {
    return `先合并若干个词，然后将${conflictWord}作为别名加入`;
  }
  if (mode === "create") {
    return "新建一个专有名词";
  }
  return `选择1到多个词，将${conflictWord}作为别名加入`;
};

const GlossaryTermBubble = ({
  asset,
  term,
}: {
  asset: GlossaryAsset;
  term?: string;
}) => {
  const displayTerm = term || asset.term;

  return (
    <Tooltip
      title={
        <div className="memory-glossary-term-tooltip">
          <div>
            <span>词</span>
            <strong>{displayTerm || "-"}</strong>
          </div>
          <div>
            <span>描述</span>
            <strong>{asset.content || "-"}</strong>
          </div>
        </div>
      }
    >
      <span className="memory-glossary-term-chip">{displayTerm || "-"}</span>
    </Tooltip>
  );
};

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
        <div className="memory-glossary-inbox-loading" aria-live="polite">
          <Spin />
          <span>{t("common.loading")}</span>
        </div>
      ) : glossaryChangeProposals.length ? (
        <div className="memory-glossary-inbox">
          <div className="memory-glossary-inbox-list">
            {glossaryChangeProposals.map((proposal, index) => {
              const isMergeProposal = Boolean(proposal.mergeFrom?.length);
              const conflictWord = getConflictWord(proposal);
              const targetGroups = proposal.backendConflictGroups || [];
              const proposalTypeText = isMergeProposal
                ? t("admin.memoryGlossaryInboxTypeMerge")
                : proposal.before
                  ? t("admin.memoryGlossaryInboxTypeUpdate")
                  : t("admin.memoryGlossaryInboxTypeAdd");
              const actionMode = actionModeMap[proposal.id] || getDefaultResolution(proposal).mode;
              const mergeStage = mergeStageMap[proposal.id] || "select";
              const createStage = createStageMap[proposal.id] || "edit";
              const activeResolution =
                resolutionMap[proposal.id] || getDefaultResolution(proposal);
              const mergeGroupIds = activeResolution.mergeGroupIds?.length
                ? activeResolution.mergeGroupIds
                : activeResolution.selectedGroupIds;
              const mergeGroups =
                activeResolution.mergeGroups?.filter((groupIds) => groupIds.length >= 2) ||
                (mergeGroupIds.length >= 2 ? [mergeGroupIds] : []);
              const mergeDrafts = syncMergeDraftsWithGroups(
                proposal,
                targetGroups,
                activeResolution.mergeDrafts,
                mergeGroups,
              );
              const mergeEditPageRaw = mergeEditPageMap[proposal.id] || 1;
              const mergeEditPage = Math.min(
                Math.max(mergeEditPageRaw, 1),
                Math.max(mergeDrafts.length, 1),
              );
              const currentMergeDraft = mergeDrafts[mergeEditPage - 1];
              const selectedMergeGroups = targetGroups.filter((group) =>
                mergeGroupIds.includes(group.id),
              );
              const unmergedGroups = targetGroups.filter(
                (group) => !mergeGroupIds.includes(group.id),
              );
              const isFullMerge =
                selectedMergeGroups.length >= 2 && unmergedGroups.length === 0;
              const canDirectConfirmMerge = isFullMerge && mergeDrafts.length === 1;
              const mergedTargetGroups: GlossaryAsset[] = mergeDrafts.map((draft, draftIndex) => ({
                ...proposal.after,
                id: `${MERGED_GROUP_OPTION_ID_PREFIX}${draft.groupIds[0] || `group-${draftIndex}`}`,
                term: draft.term || proposal.after.term,
                aliases: draft.aliases,
                content: draft.content,
              }));
              const mergedOptionIds = mergedTargetGroups.map((group) => group.id);
              const validFinalGroupIds = new Set([
                ...mergedOptionIds,
                ...unmergedGroups.map((group) => group.id),
              ]);
              const finalWriteGroupIds = activeResolution.writeGroupIds?.length
                ? activeResolution.writeGroupIds.filter((groupId) => validFinalGroupIds.has(groupId))
                : mergedOptionIds;
              const finalTargetGroups: GlossaryAsset[] = [
                ...mergedTargetGroups,
                ...unmergedGroups,
              ];
              const createDraft = {
                term: (activeResolution.newGroupTerm || "").trim(),
                aliases: activeResolution.newGroupAliases?.length
                  ? activeResolution.newGroupAliases
                  : proposal.after.aliases,
                content: (activeResolution.newGroupContent ?? proposal.after.content).trim(),
              };
              const isCreateGroupInAliases = createDraft.aliases
                .map((alias) => alias.trim())
                .some((alias) => alias && alias === createDraft.term);
              const isCreateContentSameAsTerm =
                Boolean(createDraft.term) &&
                Boolean(createDraft.content) &&
                createDraft.term === createDraft.content;
              const createWriteGroupIds = Array.from(
                new Set([NEW_GROUP_OPTION_ID, ...(activeResolution.writeGroupIds || [])]),
              );
              const createTargetGroups: GlossaryAsset[] = [
                {
                  ...proposal.after,
                  id: NEW_GROUP_OPTION_ID,
                  term: createDraft.term || proposal.after.term,
                  aliases: createDraft.aliases,
                  content: createDraft.content,
                },
                ...targetGroups,
              ];
              const getMergeGroupIdsByColor = (nextColors: Record<string, string>) => {
                const groupsByColor = new Map<string, string[]>();
                targetGroups.forEach((group) => {
                  const color = nextColors[group.id];
                  if (!color) {
                    return;
                  }
                  const current = groupsByColor.get(color) || [];
                  current.push(group.id);
                  groupsByColor.set(color, current);
                });

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
                      <div className="memory-glossary-inbox-card-line">
                        <strong>{t("admin.memoryContent")}</strong>
                        <span>{proposal.after.content || "-"}</span>
                      </div>
                      <div className="memory-glossary-action-options">
                        {(["reject", "separate", "merge", "create"] as GlossaryInboxActionMode[]).map(
                          (mode) => {
                            const disabled =
                              isSubmitting ||
                              (mode === "separate" && !targetGroups.length) ||
                              (mode === "merge" && targetGroups.length < 2);
                            return (
                              <Checkbox
                                key={mode}
                                checked={actionMode === mode}
                                disabled={disabled}
                                onChange={() => setActionMode(proposal, mode)}
                              >
                                <span className="memory-glossary-action-option-copy">
                                  <strong>{getActionModeLabel(mode, conflictWord)}</strong>
                                </span>
                              </Checkbox>
                            );
                          },
                        )}
                      </div>
                    </div>

                    <div className="memory-glossary-action-detail">
                      {actionMode === "reject" ? (
                        <div className="memory-glossary-action-empty">
                          {t("admin.memoryGlossaryInboxActionRejectDesc")}
                        </div>
                      ) : null}
                      {actionMode === "separate" ? (
                        <GlossaryGroupCards
                          groups={targetGroups}
                          selectedGroupIds={activeResolution.selectedGroupIds}
                          disabled={isSubmitting}
                          t={t}
                          stacked
                          onChange={(selectedGroupIds) =>
                            updateResolution(proposal, { selectedGroupIds })
                          }
                        />
                      ) : null}
                      {actionMode === "merge" ? (
                        mergeStage === "confirm" ? (
                          <div className="memory-glossary-merge-final-stage">
                            <div className="memory-glossary-merge-stage-title">
                              <strong>阶段3：选择一个到多个词组，把{conflictWord}加入</strong>
                              <span>可选择新合并词组，也可选择未合并词组继续写入。</span>
                            </div>
                            <GlossaryGroupCards
                              groups={finalTargetGroups}
                              selectedGroupIds={finalWriteGroupIds}
                              disabled={isSubmitting}
                              t={t}
                              stacked
                              onChange={(writeGroupIds) =>
                                updateResolution(proposal, { writeGroupIds })
                              }
                            />
                            <Button
                              className="memory-glossary-merge-request"
                              disabled={!isActionValid || isSubmitting}
                              loading={glossaryInboxSubmitting === "accept"}
                              onClick={() => submitProposalAction(proposal)}
                            >
                              确认
                            </Button>
                          </div>
                        ) : mergeStage === "edit" ? (
                          <Form layout="vertical" className="memory-glossary-create-form">
                            <div className="memory-glossary-merge-stage-title">
                              <strong>阶段2：编辑合并结果</strong>
                              <span>确认合并后的专有名词、别名和描述，再进入写入阶段。</span>
                            </div>
                            {mergeDrafts.length > 1 ? (
                              <div className="memory-glossary-merge-edit-meta">
                                <span>
                                  当前第 {mergeEditPage} 组 / 共 {mergeDrafts.length} 组
                                </span>
                                <span>
                                  {currentMergeDraft?.groupIds
                                    .map(
                                      (groupId) =>
                                        targetGroups.find((group) => group.id === groupId)?.term ||
                                        groupId,
                                    )
                                    .join(" + ")}
                                </span>
                              </div>
                            ) : null}
                            <Form.Item
                              label={t("admin.memoryGlossaryTerm")}
                              required
                              validateStatus={!currentMergeDraft?.term ? "error" : ""}
                              help={
                                !currentMergeDraft?.term
                                  ? t("admin.memoryGlossaryGroupRequired")
                                  : undefined
                              }
                            >
                              <Input
                                value={currentMergeDraft?.term}
                                disabled={isSubmitting}
                                maxLength={50}
                                showCount
                                onChange={(event) =>
                                  updateResolution(proposal, {
                                    mergeDrafts: mergeDrafts.map((draft, draftIndex) =>
                                      draftIndex === mergeEditPage - 1
                                        ? { ...draft, term: event.target.value }
                                        : draft,
                                    ),
                                  })
                                }
                              />
                            </Form.Item>
                            <Form.Item label={t("admin.memoryGlossaryAliases")}>
                              <Select
                                mode="tags"
                                value={currentMergeDraft?.aliases}
                                disabled={isSubmitting}
                                placeholder={t("admin.memoryGlossaryAliasesPlaceholder")}
                                onChange={(values) =>
                                  updateResolution(proposal, {
                                    mergeDrafts: mergeDrafts.map((draft, draftIndex) =>
                                      draftIndex === mergeEditPage - 1
                                        ? { ...draft, aliases: getUniqueTexts(values) }
                                        : draft,
                                    ),
                                  })
                                }
                              />
                            </Form.Item>
                            <Form.Item label={t("admin.memoryContent")}>
                              <Input.TextArea
                                autoSize={{ minRows: 3, maxRows: 5 }}
                                maxLength={300}
                                showCount
                                value={currentMergeDraft?.content}
                                disabled={isSubmitting}
                                onChange={(event) =>
                                  updateResolution(proposal, {
                                    mergeDrafts: mergeDrafts.map((draft, draftIndex) =>
                                      draftIndex === mergeEditPage - 1
                                        ? { ...draft, content: event.target.value }
                                        : draft,
                                    ),
                                  })
                                }
                              />
                            </Form.Item>
                            {mergeDrafts.length > 1 ? (
                              <Pagination
                                className="memory-glossary-merge-edit-pagination"
                                size="small"
                                current={mergeEditPage}
                                pageSize={1}
                                total={mergeDrafts.length}
                                showSizeChanger={false}
                                onChange={(page) =>
                                  setMergeEditPageMap((previous) => ({
                                    ...previous,
                                    [proposal.id]: page,
                                  }))
                                }
                              />
                            ) : null}
                            <Button
                              className="memory-glossary-merge-request"
                              disabled={!mergeDrafts.every((draft) => draft.term.trim()) || isSubmitting}
                              onClick={() => {
                                if (canDirectConfirmMerge) {
                                  const primaryDraft = mergeDrafts[0];
                                  submitProposalAction(proposal, {
                                    ...activeResolution,
                                    mode: "merge",
                                    mergeGroups,
                                    mergeDrafts,
                                    mergedGroupTerm: primaryDraft?.term || "",
                                    mergedGroupAliases: primaryDraft?.aliases || [],
                                    mergedGroupContent: primaryDraft?.content || "",
                                    writeGroupIds: undefined,
                                  });
                                  return;
                                }
                                const validWriteGroupIds = new Set([
                                  ...mergedOptionIds,
                                  ...unmergedGroups.map((group) => group.id),
                                ]);
                                const currentWriteGroupIds =
                                  activeResolution.writeGroupIds?.filter((groupId) =>
                                    validWriteGroupIds.has(groupId),
                                  ) || [];
                                updateResolution(proposal, {
                                  mergeGroups,
                                  mergeDrafts,
                                  mergedGroupTerm: mergeDrafts[0]?.term || "",
                                  mergedGroupAliases: mergeDrafts[0]?.aliases || [],
                                  mergedGroupContent: mergeDrafts[0]?.content || "",
                                  writeGroupIds: currentWriteGroupIds.length
                                    ? currentWriteGroupIds
                                    : mergedOptionIds,
                                });
                                setMergeStageMap((previous) => ({
                                  ...previous,
                                  [proposal.id]: "confirm",
                                }));
                              }}
                            >
                              {canDirectConfirmMerge ? "确认" : "下一步"}
                            </Button>
                          </Form>
                        ) : (
                          <div className="memory-glossary-merge-stage">
                            <div className="memory-glossary-merge-stage-title">
                              <strong>阶段1：合并阶段</strong>
                              <span>请为词组选色块，相同颜色视为合并</span>
                            </div>
                            <div className="memory-glossary-merge-stage-list">
                              {targetGroups.map((group) => {
                                const colorValue = mergeColorMap[proposal.id]?.[group.id];
                                return (
                                  <label
                                    className="memory-glossary-target-card memory-glossary-merge-stage-row"
                                    key={group.id}
                                  >
                                    <span className="memory-glossary-merge-color-control">
                                      <Select
                                        value={colorValue}
                                        allowClear
                                        disabled={isSubmitting}
                                        className="memory-glossary-color-select"
                                        labelRender={({ value }) => {
                                          const selectedColor = mergeColorOptions.find(
                                            (item) => item.value === value,
                                          );
                                          return selectedColor ? (
                                            <span className="memory-glossary-color-selected">
                                              <span
                                                style={{ backgroundColor: selectedColor.color }}
                                                aria-hidden
                                              />
                                              {selectedColor.label}
                                            </span>
                                          ) : null;
                                        }}
                                        optionRender={(option) => {
                                          const colorMeta = mergeColorOptions.find(
                                            (item) => item.value === option.value,
                                          );
                                          return (
                                            <span className="memory-glossary-color-option">
                                              <span
                                                style={{ backgroundColor: colorMeta?.color }}
                                                aria-hidden
                                              />
                                              {colorMeta?.label}
                                            </span>
                                          );
                                        }}
                                        onChange={(value) => {
                                          const currentColors = mergeColorMap[proposal.id] || {};
                                          const nextColors = Object.fromEntries(
                                            Object.entries({
                                              ...currentColors,
                                              ...(value ? { [group.id]: value } : {}),
                                            }).filter(([groupId]) => value || groupId !== group.id),
                                          );
                                          const nextMergeGroupIds = getMergeGroupIdsByColor(
                                            nextColors,
                                          );
                                          setMergeColorMap((previous) => ({
                                            ...previous,
                                            [proposal.id]: nextColors,
                                          }));
                                          const mergeGroups =
                                            buildMergeGroupsFromColors(targetGroups, nextColors);
                                          updateResolution(proposal, {
                                            selectedGroupIds: nextMergeGroupIds,
                                            mergeGroupIds: nextMergeGroupIds,
                                            mergeGroups,
                                            mergeDrafts: syncMergeDraftsWithGroups(
                                              proposal,
                                              targetGroups,
                                              activeResolution.mergeDrafts,
                                              mergeGroups,
                                            ),
                                            writeGroupIds: undefined,
                                          });
                                        }}
                                        options={mergeColorOptions.map((item) => ({
                                          value: item.value,
                                          label: item.label,
                                        }))}
                                      />
                                    </span>
                                    <span className="memory-glossary-target-main">
                                      <strong>{group.term}</strong>
                                      <span className="memory-tag-group memory-tag-group-scroll">
                                        {group.aliases.length
                                          ? group.aliases.map((alias) => (
                                              <Tag key={`${group.id}-${alias}`}>{alias}</Tag>
                                            ))
                                          : "-"}
                                      </span>
                                    </span>
                                  </label>
                                );
                              })}
                            </div>
                            <div className="memory-glossary-merge-stage-summary">
                              将合并{" "}
                              <span className="memory-glossary-merge-stage-summary-terms">
                                {targetGroups
                                  .filter((group) => mergeGroupIds.includes(group.id))
                                  .map((group, index) => {
                                    const colorValue = mergeColorMap[proposal.id]?.[group.id];
                                    const colorMeta = getMergeColorMeta(colorValue);
                                    return (
                                      <span
                                        key={group.id}
                                        className="memory-glossary-merge-stage-summary-term"
                                        style={{ color: colorMeta?.textColor || colorMeta?.color }}
                                      >
                                        {index > 0 ? "、" : ""}
                                        {group.term}
                                      </span>
                                    );
                                  })}
                              </span>
                            </div>
                            <Button
                              className="memory-glossary-merge-request"
                              disabled={isSubmitting || mergeGroupIds.length < 2}
                              onClick={() =>
                                setMergeStageMap((previous) => ({
                                  ...previous,
                                  [proposal.id]: "edit",
                                }))
                              }
                            >
                              请求合并
                            </Button>
                          </div>
                        )
                      ) : null}
                      {actionMode === "create" ? (
                        createStage === "confirm" ? (
                          <div className="memory-glossary-merge-final-stage">
                            <div className="memory-glossary-merge-stage-title">
                              <strong>阶段2：选择一个到多个词组，把{conflictWord}加入</strong>
                              <span>可选择新建词组，也可选择已有冲突词组继续写入。</span>
                            </div>
                            <GlossaryGroupCards
                              groups={createTargetGroups}
                              selectedGroupIds={createWriteGroupIds}
                              lockedGroupIds={[NEW_GROUP_OPTION_ID]}
                              disabled={isSubmitting}
                              t={t}
                              stacked
                              onChange={(writeGroupIds) =>
                                updateResolution(proposal, {
                                  writeGroupIds: Array.from(
                                    new Set([NEW_GROUP_OPTION_ID, ...writeGroupIds]),
                                  ),
                                })
                              }
                            />
                            <Button
                              className="memory-glossary-merge-request"
                              disabled={!isActionValid || isSubmitting}
                              loading={glossaryInboxSubmitting === "accept"}
                              onClick={() => submitProposalAction(proposal)}
                            >
                              确认
                            </Button>
                          </div>
                        ) : (
                          <Form layout="vertical" className="memory-glossary-create-form">
                            <div className="memory-glossary-merge-stage-title">
                              <strong>阶段1：新建一个词</strong>
                              <span>填写新词组的词、别名和描述。</span>
                            </div>
                            <Form.Item
                              label={t("admin.memoryGlossaryGroup")}
                              required
                              validateStatus={!createDraft.term ? "error" : ""}
                              help={
                                !createDraft.term
                                  ? t("admin.memoryGlossaryInboxNewGroupRequired")
                                  : undefined
                              }
                            >
                              <Input
                                value={activeResolution.newGroupTerm}
                                disabled={isSubmitting}
                                placeholder={t("admin.memoryGlossaryInboxNewGroupPlaceholder")}
                                onChange={(event) =>
                                  updateResolution(proposal, {
                                    newGroupTerm: event.target.value,
                                  })
                                }
                              />
                            </Form.Item>
                            <Form.Item
                              label={t("admin.memoryGlossaryAliases")}
                              validateStatus={isCreateGroupInAliases ? "error" : ""}
                              help={
                                isCreateGroupInAliases ? "词组归属不允许和其中一个词相同" : undefined
                              }
                            >
                              <Select
                                mode="tags"
                                value={activeResolution.newGroupAliases || []}
                                disabled={isSubmitting}
                                placeholder={t("admin.memoryGlossaryAliasesPlaceholder")}
                                onChange={(values) =>
                                  updateResolution(proposal, {
                                    newGroupAliases: getUniqueTexts(values),
                                  })
                                }
                              />
                            </Form.Item>
                            <Form.Item
                              label={t("admin.memoryContent")}
                              validateStatus={isCreateContentSameAsTerm ? "error" : ""}
                              help={
                                isCreateContentSameAsTerm ? "内容不可以和词相同" : undefined
                              }
                            >
                              <Input.TextArea
                                autoSize={{ minRows: 3, maxRows: 5 }}
                                value={
                                  activeResolution.newGroupContent ?? proposal.after.content
                                }
                                disabled={isSubmitting}
                                onChange={(event) =>
                                  updateResolution(proposal, {
                                    newGroupContent: event.target.value,
                                  })
                                }
                              />
                            </Form.Item>
                            <Button
                              className="memory-glossary-merge-request"
                              disabled={
                                !createDraft.term ||
                                isCreateGroupInAliases ||
                                isCreateContentSameAsTerm ||
                                isSubmitting
                              }
                              onClick={() => {
                                const validWriteGroupIds = new Set([
                                  NEW_GROUP_OPTION_ID,
                                  ...targetGroups.map((group) => group.id),
                                ]);
                                const currentWriteGroupIds =
                                  activeResolution.writeGroupIds?.filter((groupId) =>
                                    validWriteGroupIds.has(groupId),
                                  ) || [];
                                updateResolution(proposal, {
                                  writeGroupIds: currentWriteGroupIds.length
                                    ? currentWriteGroupIds
                                    : [NEW_GROUP_OPTION_ID],
                                });
                                setCreateStageMap((previous) => ({
                                  ...previous,
                                  [proposal.id]: "confirm",
                                }));
                              }}
                            >
                              确认新建
                            </Button>
                          </Form>
                        )
                      ) : null}
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
