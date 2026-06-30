import { type CheckpointWaitPrompt, type NormalizedThreadEvent, type ThreadEventStage } from "./types";
import { getCheckpointCommandText, t } from "./i18n";
import { getEventPayloadData, getLastItem, getNestedRecordField, getStringField } from "./fields";
import { compareNormalizedThreadEvents, getNextStageFromOperation, getStageLabel, isInactiveTerminalThreadEvent, isThreadEventAfter, toThreadEventStage } from "./threadEvents";

export function formatCheckpointOperation(value: string | undefined) {
  if (!value) {
    return undefined;
  }

  const [operationStage, ...operationParts] = value.split(".");
  const stageLabel = getStageLabel(operationStage);
  const rawAction = operationParts.join(".");
  const actionLabel =
    ({
      "": "",
      run: "",
      loop: "",
      candidate_cutover: t("selfEvolutionRun.opCandidateCutover"),
      "candidate_service.start": t("selfEvolutionRun.opCandidateServiceStart"),
      "candidate_service.stop": t("selfEvolutionRun.opCandidateServiceStop"),
    } as Record<string, string>)[rawAction] ?? rawAction.replace(/_/g, " ");
  return [stageLabel, actionLabel].filter(Boolean).join(" · ");
}

export function formatCheckpointCapability(value: string | undefined) {
  if (!value) {
    return undefined;
  }

  return ({
    patch_dataset_case: t("selfEvolutionRun.capPatchDatasetCase"),
    regenerate_dataset_case: t("selfEvolutionRun.capRegenerateDatasetCase"),
    prepare_dataset_case: t("selfEvolutionRun.capPrepareDatasetCase"),
    generate_dataset_case: t("selfEvolutionRun.capGenerateDatasetCase"),
  } as Record<string, string>)[value] ?? value.replace(/_/g, " ");
}

export function sanitizeCheckpointMessage(
  value: string,
  completedStageLabel: string | undefined,
  nextOperationLabel: string | undefined,
) {
  const cleaned = value
    .replace(/\([^)]*(?:task_id|abtest_id|summary_path|dataset_id|thread_id|\/var\/lib)[^)]*\)/gi, "")
    .replace(/\/var\/lib\/[^\s，。；、)）]+/g, "")
    .replace(/\s+/g, " ")
    .replace(/\s*([，。；、])\s*/g, "$1")
    .replace(/^[，。；、]+|[，。；、]+$/g, "")
    .trim();

  if (cleaned && cleaned.length <= 120) {
    return cleaned;
  }

  if (completedStageLabel && nextOperationLabel) {
    return t("selfEvolutionRun.checkpointStageDoneConfirmNext", { stageLabel: completedStageLabel });
  }
  if (completedStageLabel) {
    return t("selfEvolutionRun.checkpointStageDoneConfirm", { stageLabel: completedStageLabel });
  }
  return t("selfEvolutionRun.checkpointPausedConfirm");
}

export function buildCheckpointWaitPrompt(payload: Record<string, unknown> | undefined): CheckpointWaitPrompt {
  const eventData = getEventPayloadData(payload);
  const nextOperation = getNestedRecordField(eventData, ["next_op", "nextOperation", "next"]);
  const nextOperationName = getStringField(nextOperation, ["op", "operation", "name"]);
  const checkpointKind = getStringField(eventData, ["checkpoint_kind", "checkpointKind"]) ||
    getStringField(payload, ["checkpoint_kind", "checkpointKind"]);
  const capabilityLabel = formatCheckpointCapability(
    getStringField(eventData, ["capability_id", "capabilityId"]) ||
      getStringField(payload, ["capability_id", "capabilityId"]),
  );
  const artifacts = getNestedRecordField(eventData, ["artifacts", "result", "data"]);
  const messageText =
    getStringField(eventData, ["message", "text", "content"]) ||
    getStringField(payload, ["message", "text", "content"]) ||
    t("selfEvolutionRun.checkpointPausedWaiting");
  const completedStageLabel = getStageLabel(
    getStringField(eventData, ["completed_flow", "completed_stage", "stage"]) ||
      getStringField(artifacts, ["completed_flow", "stage"]),
  );
  const completedStage = toThreadEventStage(
    getStringField(eventData, ["completed_flow", "completed_stage", "stage"]) ||
      getStringField(artifacts, ["completed_flow", "stage"]),
  );
  const nextOperationLabel = formatCheckpointOperation(nextOperationName);
  const nextStage = toThreadEventStage(
    getStringField(eventData, ["next_stage", "nextStage"]) ||
      getStringField(artifacts, ["next_stage", "nextStage"]),
  ) || getNextStageFromOperation(nextOperationName);
  const command = checkpointKind === "manual_cutover"
    ? t("selfEvolutionRun.confirmCutover")
    : checkpointKind === "intent_confirmation"
      ? t("selfEvolutionRun.confirmExecute")
      : getCheckpointCommandText();
  const checkpointMessage = checkpointKind === "intent_confirmation"
    ? t("selfEvolutionRun.intentConfirmationMessage", { capability: capabilityLabel ? `「${capabilityLabel}」` : t("selfEvolutionRun.thisModification") })
    : sanitizeCheckpointMessage(messageText, completedStageLabel, nextOperationLabel);

  return {
    kind: "checkpoint",
    checkpointKind,
    message: checkpointMessage,
    completedStage,
    completedStageLabel,
    nextOperationLabel,
    nextStage,
    command,
    taskId:
      getStringField(eventData, ["completed_task_id", "task_id"]) ||
      getStringField(artifacts, ["task_id"]),
  };
}

export function isTerminalAbtestCheckpoint(prompt: CheckpointWaitPrompt | undefined) {
  return prompt?.completedStage === "abtest" && !prompt.nextStage;
}

export function buildFailureRetryPrompt(
  stage: ThreadEventStage | undefined,
  payload: Record<string, unknown> | undefined,
): CheckpointWaitPrompt {
  const eventData = getEventPayloadData(payload);
  const stageLabel = getStageLabel(stage) || t("selfEvolutionRun.currentStep");
  const rawMessage =
    getStringField(eventData, ["message", "error_message", "error", "detail"]) ||
    getStringField(payload, ["message", "error_message", "error", "detail"]);
  const errorCode =
    getStringField(eventData, ["error_code", "code"]) ||
    getStringField(payload, ["error_code", "code"]);
  const reason = getFriendlyFailureReason(errorCode, rawMessage);
  const taskId =
    getStringField(eventData, ["task_id", "apply_id", "run_id", "eval_id", "dataset_id"]) ||
    getStringField(payload, ["task_id"]);

  return {
    kind: "failure",
    message: t("selfEvolutionRun.stageFailedMessage", { stageLabel, reason }),
    completedStageLabel: stageLabel,
    nextStage: stage,
    command: t("selfEvolutionRun.retryCommand"),
    taskId,
  };
}

export function getFriendlyFailureReason(errorCode: string | undefined, rawMessage: string | undefined) {
  if (errorCode === "REPORT_ACTIONS_NOT_READY" || rawMessage?.includes("below apply confidence/validity thresholds")) {
    return t("selfEvolutionRun.failureReasonReportNotReady");
  }
  if (errorCode === "RAG_CALL_FAILED" || rawMessage?.includes("chat service failed")) {
    return t("selfEvolutionRun.failureReasonRagCallFailed");
  }
  if (rawMessage) {
    return rawMessage;
  }
  if (errorCode) {
    return t("selfEvolutionRun.failureReasonErrorCode", { errorCode });
  }
  return t("selfEvolutionRun.failureReasonGeneric");
}

export function getPendingCheckpointWaitPrompt(events: NormalizedThreadEvent[]) {
  const hasInactiveTerminalEvent = events.some(isInactiveTerminalThreadEvent);
  if (hasInactiveTerminalEvent) {
    return undefined;
  }

  const checkpointEvents = events
    .filter((event) => event.type === "checkpoint.wait" && event.checkpointWait)
    .sort(compareNormalizedThreadEvents);
  const latestCheckpointEvent = getLastItem(checkpointEvents);

  if (!latestCheckpointEvent?.checkpointWait) {
    return undefined;
  }

  const nextStage = latestCheckpointEvent.checkpointWait.nextStage;
  const hasContinued = events.some((event) => {
    const isLaterEvent = isThreadEventAfter(latestCheckpointEvent, event);
    if (!isLaterEvent) {
      return false;
    }
    if (
      event.type === "checkpoint.continue" ||
      event.type === "checkpoint.rewind" ||
      event.type === "checkpoint.cancel"
    ) {
      return true;
    }
    if (event.type.startsWith("autooperator.")) {
      return false;
    }
    if (nextStage && event.stage === nextStage) {
      return true;
    }
    return Boolean(event.stage);
  });

  return hasContinued ? undefined : latestCheckpointEvent.checkpointWait;
}
