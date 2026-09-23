import { Drawer } from "antd";
import { forwardRef, Ref, useImperativeHandle, useRef, useState } from "react";

import ImportTaskList from "../ImportTaskList";

export interface IImportTaskManageRef {
  handleOpen: (data?: { dataset_id?: string }) => void;
}

interface IProps {
  onClose: (hasSuspended?: boolean) => void;
}

const ImportTaskManage = (props: IProps, ref: Ref<unknown> | undefined) => {
  const [data, setData] = useState<{ dataset_id?: string }>({});
  const [visible, setVisible] = useState(false);
  const hasSuspendedRef = useRef(false);
  const { onClose } = props;

  const handleOpen = (data: { dataset_id?: string } = {}) => {
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
        datasetId={data.dataset_id ?? ""}
        onClose={handleClose}
        onSuspendSuccess={() => {
          hasSuspendedRef.current = true;
        }}
      />
    </Drawer>
  );
};

export default forwardRef(ImportTaskManage);
