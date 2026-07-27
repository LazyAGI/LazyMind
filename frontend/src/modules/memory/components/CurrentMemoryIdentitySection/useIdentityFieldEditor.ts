import { message } from "antd";
import {
  type Dispatch,
  type SetStateAction,
  useCallback,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import type { CurrentMemorySnapshot } from "../../currentMemoryApi";
import { isCurrentMemoryConflict } from "../../currentMemoryViewModel";

export type IdentityFieldValue = string | null | string[];

export interface IdentityField<TPatch> {
  path: string;
  sectionPath: string;
  sectionLabel: string;
  label: string;
  value: IdentityFieldValue;
  valueType: "string" | "string-list";
  buildPatch: (value: IdentityFieldValue) => TPatch;
  buildRemovePatch?: (value: string) => TPatch;
  buildClearPatch: () => TPatch;
}

interface UseIdentityFieldEditorOptions<TDocument, TPatch> {
  kind: "soul" | "profile";
  load: () => Promise<CurrentMemorySnapshot<TDocument>>;
  save: (patch: TPatch) => Promise<CurrentMemorySnapshot<TDocument>>;
  setSnapshot: Dispatch<
    SetStateAction<CurrentMemorySnapshot<TDocument> | null>
  >;
}

const normalizeDraftValue = <TPatch,>(
  field: IdentityField<TPatch>,
  value: IdentityFieldValue,
): IdentityFieldValue => {
  if (field.valueType === "string") {
    return String(value || "").trim();
  }
  return String(value || "").trim();
};

export const useIdentityFieldEditor = <TDocument, TPatch>({
  kind,
  load,
  save,
  setSnapshot,
}: UseIdentityFieldEditorOptions<TDocument, TPatch>) => {
  const { t } = useTranslation();
  const [editingField, setEditingField] =
    useState<IdentityField<TPatch> | null>(null);
  const [draftValue, setDraftValue] = useState<IdentityFieldValue>("");
  const draftValueRef = useRef<IdentityFieldValue>("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [conflict, setConflict] = useState(false);
  const pendingPatchRef = useRef<TPatch | null>(null);

  const updateDraftValue = useCallback((value: IdentityFieldValue) => {
    draftValueRef.current = value;
    setDraftValue(value);
  }, []);

  const beginEdit = useCallback(
    (field: IdentityField<TPatch>) => {
      setEditingField(field);
      updateDraftValue(Array.isArray(field.value) ? "" : field.value);
      setSaveError("");
      setConflict(false);
    },
    [updateDraftValue],
  );

  const clearEditor = useCallback(() => {
    setEditingField(null);
    updateDraftValue("");
    setSaveError("");
    setConflict(false);
  }, [updateDraftValue]);

  const cancelEdit = useCallback(() => {
    if (!saving) {
      clearEditor();
    }
  }, [clearEditor, saving]);

  const savePatch = useCallback(
    async (patch: TPatch, clearOnSuccess = true) => {
      if (saving) {
        return;
      }
      pendingPatchRef.current = patch;
      setSaving(true);
      setSaveError("");
      setConflict(false);
      try {
        setSnapshot(await save(patch));
        if (clearOnSuccess) {
          clearEditor();
        }
        message.success(t("admin.memoryCurrentSaveSuccess"));
      } catch (error) {
        console.error(`Save ${kind} memory field failed:`, error);
        if (isCurrentMemoryConflict(error)) {
          setConflict(true);
        } else {
          const errorMessage = getLocalizedErrorMessage(error);
          setSaveError(errorMessage);
          message.error(errorMessage);
        }
      } finally {
        setSaving(false);
      }
    },
    [clearEditor, kind, save, saving, setSnapshot, t],
  );

  const saveField = useCallback(async () => {
    if (!editingField || saving) {
      return;
    }
    const nextValue = normalizeDraftValue(
      editingField,
      draftValueRef.current,
    );
    if (
      editingField.valueType === "string-list" &&
      !String(nextValue).trim()
    ) {
      setSaveError(t("admin.memoryCurrentRequiredField"));
      return;
    }

    await savePatch(editingField.buildPatch(nextValue));
  }, [editingField, savePatch, saving, t]);

  const retrySave = useCallback(async () => {
    if (pendingPatchRef.current) {
      await savePatch(pendingPatchRef.current);
    }
  }, [savePatch]);

  const reloadConflictSnapshot = useCallback(async () => {
    if (saving) {
      return;
    }
    setSaving(true);
    setSaveError("");
    try {
      setSnapshot(await load());
      setConflict(false);
    } catch (error) {
      setSaveError(getLocalizedErrorMessage(error));
    } finally {
      setSaving(false);
    }
  }, [load, saving, setSnapshot]);

  return {
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
  };
};
