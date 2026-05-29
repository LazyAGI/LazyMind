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
import { IMPORT_TASK_POLL_INTERVAL, IMPORT_TASK_FAILED_STATES, IMPORT_TASK_RUNNING_STATES, IMPORT_TASK_SUCCESS_STATES } from "@/modules/knowledge/constants/common";

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

const RUNNING_STATES = IMPORT_TASK_RUNNING_STATES;
const SUCCESS_STATES = IMPORT_TASK_SUCCESS_STATES;
const FAILED_STATES = IMPORT_TASK_FAILED_STATES;

export const TaskTabInfo = [
  { id: TaskTab.Running, titleKey: "knowledge.importRunning", taskStates: RUNNING_STATES },
  { id: TaskTab.Successed, titleKey: "knowledge.importSuccessTitle", taskStates: SUCCESS_STATES },
  { id: TaskTab.Failed, titleKey: "knowledge.importFailedTitle", taskStates: FAILED_STATES },
];

const ImportTaskList = (props: IProps) => {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [total, setTotal] = useState(0);
  const [dataSource, setDataSource] = useState([]);
  const [tab, setTab] = useState(TaskTab.Running);
  const pollingRef = useRef(new Polling());
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
  }) => {
    const { page = 1, size = pageSize, currentTab = tab } = params || {};
    setPage(page);
    setPageSize(size);
    pollingRef.current.cancel();

    const updateTableData = ({ data = {} }: { data?: { tasks?: any[] } }) => {
      const allTasks: any[] = data.tasks || [];
      const states = TaskTabInfo.find((item) => item.id === currentTab)?.taskStates || [];
      const filtered = allTasks.filter((t) => states.includes(t.task_state));
      const start = (page - 1) * size;
      setTotal(filtered.length);
      setDataSource(filtered.slice(start, start + size) as any);

      if (currentTab === TaskTab.Running && filtered.length === 0) {
        pollingRef.current.cancel();
      }
    };

    if (currentTab !== TaskTab.Running) {
      TaskServiceApi()
        .listTasks(datasetId)
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
      request: () => TaskServiceApi().listTasks(datasetId),
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
    getTableData({ currentTab: v });
  };

  function suspendTaskFn(cvm: any) {
    TaskServiceApi()
      .suspendTask(datasetId, cvm?.task_id)
      .then(() => {
        message.success(t("knowledge.taskSuspendSuccess"));
        onSuspendSuccess?.();
        getTableData({ currentTab: tab });
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
        const isRunning = RUNNING_STATES.includes(record.task_state);
        const endTime = isRunning
          ? undefined
          : record.finish_time || record.create_time || time;
        return (
          <ElapsedTime
            startTime={record.start_time || time}
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
        return (
          <>
            {tab === TaskTab.Running && !isOnlyRead && (
              <a onClick={() => suspendTaskFn(record)}>{t("knowledge.suspend")}</a>
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
          getTableData({ page: pagination.current, size: pagination.pageSize });
        }}
      />
    </div>
  );
};

export default ImportTaskList;
