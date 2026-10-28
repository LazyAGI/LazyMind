import { create } from "zustand";
import { subscribeWithSelector } from "zustand/middleware";
import { Dataset, DatasetAclEnum } from "@/api/generated/knowledge-client";
import { KnowledgeBaseServiceApi } from "../utils/request";

interface DatasetPermissionState {
  currentDataset: Dataset | null;
  requestedDatasetId: string | null;
  datasetLoading: boolean;
  datasetError: string;

  loadDataset: (datasetId: string, refresh?: boolean) => Promise<Dataset>;

  setCurrentDataset: (dataset: Dataset | null) => void;

  hasWritePermission: () => boolean;

  hasOnlyReadPermission: () => boolean;

  hasUploadPermission: () => boolean;

  clearDataset: () => void;

  getDatasetDetail: () => Dataset | null;
}

export const useDatasetPermissionStore = create<DatasetPermissionState>()(
  subscribeWithSelector((set, get) => {
    let generation = 0;
    let pending: { id: string; promise: Promise<Dataset> } | undefined;
    return {
      currentDataset: null,
      requestedDatasetId: null,
      datasetLoading: false,
      datasetError: "",

      loadDataset: (datasetId, refresh = false) => {
        if (pending?.id === datasetId) return pending.promise;
        const current = get().currentDataset;
        if (!refresh && current?.dataset_id === datasetId) return Promise.resolve(current);
        const request = ++generation;
        set({ currentDataset: current?.dataset_id === datasetId ? current : null, requestedDatasetId: datasetId, datasetLoading: true, datasetError: "" });
        const promise: Promise<Dataset> = KnowledgeBaseServiceApi().datasetServiceGetDataset({ dataset: datasetId })
          .then(response => {
            const dataset = response.data as Dataset;
            if (dataset.dataset_id !== datasetId) throw new Error("知识库详情与当前页面不匹配，请重试");
            if (request === generation) set({ currentDataset: dataset, datasetLoading: false, datasetError: "" });
            return dataset;
          })
          .catch(error => {
            if (request === generation) set({ datasetLoading: false, datasetError: error instanceof Error ? error.message : "知识库加载失败，请重试" });
            throw error;
          })
          .finally(() => { if (pending?.promise === promise) pending = undefined; });
        pending = { id: datasetId, promise };
        return promise;
      },

      setCurrentDataset: (dataset: Dataset | null) => {
        generation++;
        pending = undefined;
        set({ currentDataset: dataset, requestedDatasetId: dataset?.dataset_id || null, datasetLoading: false, datasetError: "" });
      },

      hasWritePermission: () => {
        const { currentDataset } = get();
        return (
          currentDataset?.acl?.includes(DatasetAclEnum.DatasetWrite) ?? false
        );
      },

      hasOnlyReadPermission: () => {
        const { currentDataset } = get();
        if (!currentDataset?.acl || currentDataset.acl.length === 0) {
          return false;
        }
        return (
          currentDataset.acl.includes(DatasetAclEnum.DatasetRead) &&
          !currentDataset.acl.includes(DatasetAclEnum.DatasetWrite) &&
          !currentDataset.acl.includes(DatasetAclEnum.DatasetUpload)
        );
      },

      hasUploadPermission: () => {
        const { currentDataset } = get();
        return (
          currentDataset?.acl?.includes(DatasetAclEnum.DatasetUpload) ?? false
        );
      },

      clearDataset: () => {
        generation++;
        pending = undefined;
        set({ currentDataset: null, requestedDatasetId: null, datasetLoading: false, datasetError: "" });
      },

      getDatasetDetail: () => {
        return get().currentDataset;
      },
    };
  }),
);
