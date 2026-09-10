import { FileState } from "@/modules/knowledge/constants/common";
import { JobServiceApi } from "./request";
import {
  JobJobStateEnum,
} from "@/api/generated/knowledge-client";

export const compatibleUploadConfig = () => {
  return {
    ECompatibleFileState: {
      UploadPending: FileState.UPLOAD_PENDING,
      Uploading: FileState.UPLOADING,
      Success: FileState.PARSE_PENDING,
      Fail: FileState.FAIL,
      Cancel: FileState.CANCEL,
    },
    ECompatiblTaskState: {
      Uploading: JobJobStateEnum.Creating,
      JobStarted: JobJobStateEnum.Parsing,
      Fail: JobJobStateEnum.Failed,
      Cancel: JobJobStateEnum.Cancelled,
      Success: JobJobStateEnum.Succeeded,
    },
    CompatibleAPI: {
      batchGetPresignURL: JobServiceApi().jobServiceBatchPresignUploadFileURL,
      getMultipartPresignURL:
        JobServiceApi().jobServicePresignMultipartUploadFileURL,
      completeMultipart: JobServiceApi().jobServiceCompleteMultipartUploadFile,
      startImportTask: JobServiceApi().jobServiceStartJob,
      cancelTaskWithKeepalive: JobServiceApi().jobServiceCancelJob,
    },
  };
};
