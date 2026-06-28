import { message, Modal, Radio, Table, Tooltip } from "antd";
import { CloseOutlined } from "@ant-design/icons";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import moment from "moment";

import ElapsedTime from "../ElapsedTime";
import "./index.scss";
import Polling from "@/modules/knowledge/utils/polling";
import { TaskServiceApi } from "@/modules/knowledge/utils/request";
import { useDatasetPermissionStore } from "@/modules/knowledge/store/dataset_permission";
import { getLocalizedTablePagination } from "@/components/ui/pagination";
import { localizeErrorCode } from "@/components/request";
import { IMPORT_TASK_POLL_INTERVAL } from "@/modules/knowledge/constants/common";

interface IProps {
  datasetId: string;
  onClose: () => void;
  onSuspendSuccess?: () => void;
}

export enum TaskTab {
  Running = "1",
  Successed = "2",
  Failed = "3",
}

// Maps each tab to the task_state value sent to the backend.
const TAB_TO_TASK_STATUS: Record<TaskTab, string> = {
  [TaskTab.Running]: "running",
  [TaskTab.Successed]: "success",
  [TaskTab.Failed]: "failed",
};

export const TaskTabInfo = [
  { id: TaskTab.Running, titleKey: "knowledge.importRunning" },
  { id: TaskTab.Successed, titleKey: "knowledge.importSuccessTitle" },
  { id: TaskTab.Failed, titleKey: "knowledge.importFailedTitle" },
];

const ImportTaskList = (props: IProps) => {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [total, setTotal] = useState(0);
  const [dataSource, setDataSource] = useState([]);
  const [tab, setTab] = useState(TaskTab.Running);
  // pageTokens[i] is the token to fetch page i+1 (index 0 = first page, no token needed).
  const [pageTokens, setPageTokens] = useState<(string | undefined)[]>([undefined]);
  const pollingRef = useRef(new Polling());
  const suspendingTaskIdsRef = useRef(new Set<string>());
  const [suspendingTaskIds, setSuspendingTaskIds] = useState<Set<string>>(
    () => new Set(),
  );
  const { datasetId, onClose, onSuspendSuccess } = props;
  const hasOnlyReadPermission = useDatasetPermissionStore((state) =>
    state.hasOnlyReadPermission(),
  );
  const hasUploadPermission = useDatasetPermissionStore((state) =>
    state.hasUploadPermission(),
  );
  const hasWritePermission = useDatasetPermissionStore((state) =>
    state.hasWritePermission(),
  );
  const isOnlyRead =
    (hasOnlyReadPermission || hasUploadPermission) && !hasWritePermission;

  const getTableData = (params?: {
    page?: number;
    size?: number;
    currentTab?: TaskTab;
    tokens?: (string | undefined)[];
  }) => {
    const { page = 1, size = pageSize, currentTab = tab, tokens = pageTokens } = params || {};
    setPage(page);
    setPageSize(size);
    pollingRef.current.cancel();

    const taskStatus = TAB_TO_TASK_STATUS[currentTab];
    const pageToken = getTaskPageToken(page, size, tokens);

    const updateTableData = ({ data = {} }: { data?: { tasks?: any[]; total_size?: number; next_page_token?: string } }) => {
      const tasks: any[] = data.tasks || [];
      setTotal(data.total_size ?? 0);
      setDataSource(tasks as any);

      // Store the next page token so the user can navigate forward.
      if (data.next_page_token) {
        setPageTokens((prev) => {
          const next = [...prev];
          next[page] = data.next_page_token;
          return next;
        });
      }

      if (currentTab === TaskTab.Running && tasks.length === 0) {
        pollingRef.current.cancel();
      }
    };

    const requestFn = () =>
      TaskServiceApi().listTasks(datasetId, { taskStatus, pageSize: size, pageToken });

    if (currentTab !== TaskTab.Running) {
      requestFn()
        .then(updateTableData)
        .catch((err) => {
          console.error(err);
          setTotal(0);
          setDataSource([]);
        });
      return;
    }

    pollingRef.current.start({
      interval: IMPORT_TASK_POLL_INTERVAL,
      request: requestFn,
      onSuccess: updateTableData,
      onError: (err) => {
        console.error(err);
        setTotal(0);
        setDataSource([]);
      },
    });
  };

  const changeTab = (v: TaskTab) => {
    setDataSource([]);
    setTab(v);
    setPage(1);
    const freshTokens = [undefined] as (string | undefined)[];
    setPageTokens(freshTokens);
    getTableData({ currentTab: v, page: 1, tokens: freshTokens });
  };

  function updateSuspendingTaskIds() {
    setSuspendingTaskIds(new Set(suspendingTaskIdsRef.current));
  }

  function suspendTaskFn(record: any) {
    const taskId = record?.task_id;
    if (!taskId || suspendingTaskIdsRef.current.has(taskId)) {
      return;
    }

    suspendingTaskIdsRef.current.add(taskId);
    updateSuspendingTaskIds();

    TaskServiceApi()
      .suspendTask(datasetId, taskId)
      .then(() => {
        message.success(t("knowledge.taskSuspendSuccess"));
        onSuspendSuccess?.();
        getTableData({ currentTab: tab });
      })
      .finally(() => {
        suspendingTaskIdsRef.current.delete(taskId);
        updateSuspendingTaskIds();
      });
  }

  function resumeTaskFn(cvm: any) {
    TaskServiceApi()
      .resumeTask(datasetId, cvm?.task_id)
      .then(() => {
        message.success(t("knowledge.taskRetrySuccess"));
        getTableData({ currentTab: tab });
      });
  }

  function deleteTaskFn(cvm: any) {
    TaskServiceApi()
      .deleteTask(datasetId, cvm?.task_id)
      .then(() => {
        message.success(t("knowledge.taskDeleteSuccess"));
        getTableData({ currentTab: tab });
      });
  }

  function confirmDelete(cvm: any) {
    Modal.confirm({
      title: t("knowledge.confirmDeleteTitle"),
      content: t("knowledge.confirmDeleteTaskContent"),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      onOk: () => {
        deleteTaskFn(cvm);
      },
    });
  }

  const columns = [
    {
      title: t("knowledge.createTime"),
      dataIndex: "create_time",
      width: 200,
      render: (text: number) => {
        return moment(text).format("YYYY-MM-DD HH:mm:ss");
      },
    },
    {
      title: t("knowledge.creatingName"),
      dataIndex: "display_name",
      width: 200,
      render: (text: string) => {
        return (
          <Tooltip title={text}>
            <div className="ellipsis-text">{text || t("knowledge.importing")}</div>
          </Tooltip>
        );
      },
    },
    {
      title: t("knowledge.creator"),
      dataIndex: "creator",
      width: 120,
    },
    {
      title: t("knowledge.dataSource"),
      dataIndex: "data_source_type",
      width: 115,
      render: () => {
        return t("knowledge.localFile");
      },
    },
    {
      title: t("knowledge.elapsedUsed"),
      dataIndex: "create_time",
      width: 105,
      render: (time: string, record: any) => {
        const isRunning = tab === TaskTab.Running;
        const startTime = getElapsedStartTime({
          startTime: record.start_time,
          fallbackTime: record.create_time || time,
          isRunning,
        });
        const endTime = isRunning
          ? undefined
          : record.finish_time || record.create_time || time;
        return (
          <ElapsedTime
            startTime={startTime}
            endTime={endTime}
          />
        );
      },
    },
    ...(tab === TaskTab.Failed
      ? [
          {
            title: t("knowledge.parseTaskError"),
            dataIndex: "err_msg",
            width: 200,
            render: (text: string) => {
              const display = localizeErrorCode(text, "-");
              return (
                <Tooltip title={display}>
                  <div className="ellipsis-text">{display}</div>
                </Tooltip>
              );
            },
          },
        ]
      : []),
    {
      title: t("common.actions"),
      key: "action",
      width: 140,
      render: (record: any) => {
        const isSuspending = suspendingTaskIds.has(record?.task_id);

        return (
          <>
            {tab === TaskTab.Running && !isOnlyRead && (
              <a
                className={isSuspending ? "import-task-action-disabled" : undefined}
                onClick={() => suspendTaskFn(record)}
              >
                {t("knowledge.suspend")}
              </a>
            )}
            {tab === TaskTab.Failed && !isOnlyRead && (
              <a
                style={{ marginRight: 6 }}
                onClick={() => resumeTaskFn(record)}
              >
                {t("knowledge.retry")}
              </a>
            )}
            {tab === TaskTab.Failed && !isOnlyRead && (
              <a onClick={() => confirmDelete(record)}>{t("common.delete")}</a>
            )}
          </>
        );
      },
    },
  ];

  useEffect(() => {
    getTableData();
    return () => {
      pollingRef.current.cancel();
    };
  }, []);

  return (
    <div className="import-task-list">
      <div className="header">
        <span className="import-task-list-title">{t("knowledge.importTaskPanelTitle")}</span>
        <CloseOutlined onClick={onClose} className="closeIcon" />
      </div>
      <Radio.Group
        value={tab}
        className="tab"
        onChange={(e) => changeTab(e.target.value)}
      >
        {TaskTabInfo.map((item) => {
          return (
            <Radio.Button value={item.id} key={item.id}>
              {t(item.titleKey)}
            </Radio.Button>
          );
        })}
      </Radio.Group>
      <Table
        columns={columns}
        dataSource={dataSource}
        rowKey="task_id"
        pagination={getLocalizedTablePagination({
          current: page,
          pageSize,
          total,
        }, t)}
        onChange={(pagination) => {
          const newPage = pagination.current ?? 1;
          const newSize = pagination.pageSize ?? pageSize;
          if (newSize !== pageSize) {
            // Page size changed: token cache is invalid, reset and refetch from page 1.
            const freshTokens = [undefined] as (string | undefined)[];
            setPageTokens(freshTokens);
            getTableData({ page: 1, size: newSize, tokens: freshTokens });
          } else {
            getTableData({ page: newPage, size: newSize });
          }
        }}
      />
    </div>
  );
};

function getElapsedStartTime({
  startTime,
  fallbackTime,
  isRunning,
}: {
  startTime?: number | string;
  fallbackTime?: number | string;
  isRunning: boolean;
}) {
  const parsedStartTime = parseTaskTime(startTime);
  if (!parsedStartTime) {
    return fallbackTime;
  }

  if (isRunning && parsedStartTime.isAfter(moment())) {
    return fallbackTime;
  }

  return startTime;
}

function getTaskPageToken(
  page: number,
  pageSize: number,
  tokens: (string | undefined)[],
) {
  if (page <= 1) {
    return undefined;
  }

  return tokens[page - 1] || String((page - 1) * pageSize);
}

function parseTaskTime(value?: number | string) {
  if (value === undefined || value === null || value === "" || value === 0 || value === "0") {
    return null;
  }
  const text = String(value).trim();
  const numeric = Number(text);
  if (Number.isFinite(numeric) && text !== "") {
    if (numeric >= 1_000_000_000 && numeric < 1_000_000_000_000) {
      return moment(numeric * 1000);
    }
    if (numeric >= 1_000_000_000_000) {
      return moment(numeric);
    }
  }
  const parsed = moment(text);
  return parsed.isValid() ? parsed : null;
}

export default ImportTaskList;
