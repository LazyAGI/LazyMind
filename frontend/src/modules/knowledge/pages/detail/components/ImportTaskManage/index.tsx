import { Drawer } from "antd";
import { forwardRef, Ref, useImperativeHandle, useRef, useState } from "react";

import ImportTaskList, { TaskTab } from "../ImportTaskList";
import type { ImportTaskState } from "../../importTaskFailures";

interface OpenData { dataset_id?: string; initialTab?: TaskTab }

export interface IImportTaskManageRef {
  handleOpen: (data?: OpenData) => void;
}

interface IProps {
  onClose: (hasSuspended?: boolean) => void;
  onRetrySuccess?: (task: ImportTaskState) => void;
}

const ImportTaskManage = (props: IProps, ref: Ref<unknown> | undefined) => {
  const [data, setData] = useState<OpenData>({});
  const [visible, setVisible] = useState(false);
  const hasSuspendedRef = useRef(false);
  const { onClose } = props;

  const handleOpen = (data: OpenData = {}) => {
    setData(data);
    setVisible(true);
    hasSuspendedRef.current = false;
  };

  const handleClose = () => {
    setData({});
    setVisible(false);
    onClose(hasSuspendedRef.current);
    hasSuspendedRef.current = false;
  };

  useImperativeHandle(ref, () => ({
    handleOpen,
  }));

  return (
    <Drawer
      open={visible}
      width={900}
      closable={false}
      maskClosable
      destroyOnHidden
      onClose={handleClose}
    >
      <ImportTaskList
        key={`${data.dataset_id}:${data.initialTab ?? TaskTab.Running}`}
        datasetId={data.dataset_id ?? ""}
        initialTab={data.initialTab}
        onClose={handleClose}
        onRetrySuccess={props.onRetrySuccess}
        onSuspendSuccess={() => {
          hasSuspendedRef.current = true;
        }}
      />
    </Drawer>
  );
};

export default forwardRef(ImportTaskManage);
