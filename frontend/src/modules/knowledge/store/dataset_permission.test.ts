import { describe, it, expect, beforeEach } from "vitest";
import { act } from "@testing-library/react";
import { DatasetAclEnum } from "@/api/generated/knowledge-client";
import { useDatasetPermissionStore } from "./dataset_permission";

describe("useDatasetPermissionStore", () => {
  beforeEach(() => {
    act(() => {
      useDatasetPermissionStore.getState().clearDataset();
    });
  });

  it("starts with no current dataset", () => {
    expect(useDatasetPermissionStore.getState().currentDataset).toBeNull();
    expect(useDatasetPermissionStore.getState().getDatasetDetail()).toBeNull();
  });

  it("setCurrentDataset updates the current dataset and getDatasetDetail reflects it", () => {
    const dataset = { dataset_id: "1", acl: [DatasetAclEnum.DatasetRead] };

    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset(dataset as any);
    });

    expect(useDatasetPermissionStore.getState().currentDataset).toEqual(dataset);
    expect(useDatasetPermissionStore.getState().getDatasetDetail()).toEqual(dataset);
  });

  it("hasWritePermission is true only when acl contains DatasetWrite", () => {
    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset({
        acl: [DatasetAclEnum.DatasetRead, DatasetAclEnum.DatasetWrite],
      } as any);
    });
    expect(useDatasetPermissionStore.getState().hasWritePermission()).toBe(true);

    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset({
        acl: [DatasetAclEnum.DatasetRead],
      } as any);
    });
    expect(useDatasetPermissionStore.getState().hasWritePermission()).toBe(false);
  });

  it("hasWritePermission is false when there is no current dataset or acl", () => {
    expect(useDatasetPermissionStore.getState().hasWritePermission()).toBe(false);

    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset({} as any);
    });
    expect(useDatasetPermissionStore.getState().hasWritePermission()).toBe(false);
  });

  it("hasUploadPermission is true only when acl contains DatasetUpload", () => {
    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset({
        acl: [DatasetAclEnum.DatasetUpload],
      } as any);
    });
    expect(useDatasetPermissionStore.getState().hasUploadPermission()).toBe(true);

    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset({
        acl: [DatasetAclEnum.DatasetRead],
      } as any);
    });
    expect(useDatasetPermissionStore.getState().hasUploadPermission()).toBe(false);
  });

  it("hasOnlyReadPermission is true only when acl has read but not write/upload", () => {
    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset({
        acl: [DatasetAclEnum.DatasetRead],
      } as any);
    });
    expect(useDatasetPermissionStore.getState().hasOnlyReadPermission()).toBe(true);
  });

  it("hasOnlyReadPermission is false when acl also has write or upload", () => {
    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset({
        acl: [DatasetAclEnum.DatasetRead, DatasetAclEnum.DatasetWrite],
      } as any);
    });
    expect(useDatasetPermissionStore.getState().hasOnlyReadPermission()).toBe(false);

    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset({
        acl: [DatasetAclEnum.DatasetRead, DatasetAclEnum.DatasetUpload],
      } as any);
    });
    expect(useDatasetPermissionStore.getState().hasOnlyReadPermission()).toBe(false);
  });

  it("hasOnlyReadPermission is false when acl is empty or missing", () => {
    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset({ acl: [] } as any);
    });
    expect(useDatasetPermissionStore.getState().hasOnlyReadPermission()).toBe(false);

    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset({} as any);
    });
    expect(useDatasetPermissionStore.getState().hasOnlyReadPermission()).toBe(false);
  });

  it("clearDataset resets the current dataset to null", () => {
    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset({ dataset_id: "1" } as any);
      useDatasetPermissionStore.getState().clearDataset();
    });

    expect(useDatasetPermissionStore.getState().currentDataset).toBeNull();
  });
});
