import { forwardRef, useImperativeHandle, useState } from "react";
import { Modal, Input } from "antd";
import { useTranslation } from "react-i18next";
import "./index.scss";

interface ModalInfo {
  id: string;
  title: string;
  content: string;
  confirmText: string;
}

interface ForwardProps {
  onClick: (id: string) => void;
}

export interface ConfirmImperativeProps {
  onOpen: (data: ModalInfo) => void;
}

const ConfirmModalComponent = forwardRef<ConfirmImperativeProps, ForwardProps>(
  ({ onClick }, ref) => {
    const { t } = useTranslation();
    const [visible, setVisible] = useState(false);
    const [modalInfo, setModalInfo] = useState<ModalInfo | null>();
    const [value, setValue] = useState("");
    const [errorText, setErrorText] = useState("");
    useImperativeHandle(ref, () => ({
      onOpen,
    }));

    const onOpen = (data: ModalInfo) => {
      setModalInfo(data);
      setValue("");
      setErrorText("");
      setVisible(true);
    };
    const onCancel = () => {
      setModalInfo(null);
      setErrorText("");
      setValue("");
      setVisible(false);
    };

    const isSuccess = () => {
      if (!value) {
        setErrorText(t("knowledge.inputRequired"));
        return false;
      }
      if (value !== modalInfo?.confirmText) {
        setErrorText(t("knowledge.inputMismatch"));
        return false;
      }
      setErrorText("");
      return true;
    };

    return (
      <Modal
        title={modalInfo?.title}
        open={visible}
        maskClosable={false}
        onCancel={onCancel}
        okText={t("common.confirm")}
        cancelText={t("common.cancel")}
        okType="danger"
        okButtonProps={{ disabled: value !== modalInfo?.confirmText }}
        onOk={() => {
          if (!isSuccess()) {
            return false;
          }
          onClick(modalInfo?.id || "");
          onCancel();
        }}
      >
        <div className="confirm-container">
          <p className="content">{modalInfo?.content}</p>
          <p className="confirm-text">
            “<span>{modalInfo?.confirmText}</span>”
          </p>

          <Input
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onBlur={() => {
              isSuccess();
            }}
            onFocus={() => setErrorText("")}
          />

          <p className="error-tip">{errorText}</p>
        </div>
      </Modal>
    );
  },
);

export default ConfirmModalComponent;
