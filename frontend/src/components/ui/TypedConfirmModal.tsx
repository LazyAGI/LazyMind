import { forwardRef, useImperativeHandle, useState } from 'react';
import { Modal, Input } from 'antd';
import { useTranslation } from 'react-i18next';
import './TypedConfirmModal.scss';

export interface TypedConfirmModalInfo {
  id: string;
  title: string;
  content: string;
  confirmText: string;
}

export interface TypedConfirmModalRef {
  onOpen: (data: TypedConfirmModalInfo) => void;
}

interface TypedConfirmModalProps {
  onClick: (id: string) => void;
}

const TypedConfirmModal = forwardRef<TypedConfirmModalRef, TypedConfirmModalProps>(
  ({ onClick }, ref) => {
    const { t } = useTranslation();
    const [visible, setVisible] = useState(false);
    const [modalInfo, setModalInfo] = useState<TypedConfirmModalInfo | null>();
    const [value, setValue] = useState('');
    const [errorText, setErrorText] = useState('');

    const onOpen = (data: TypedConfirmModalInfo) => {
      setModalInfo(data);
      setValue('');
      setErrorText('');
      setVisible(true);
    };

    useImperativeHandle(ref, () => ({
      onOpen,
    }));

    const onCancel = () => {
      setModalInfo(null);
      setErrorText('');
      setValue('');
      setVisible(false);
    };

    const isSuccess = () => {
      if (!value) {
        setErrorText(t('common.pleaseInput'));
        return false;
      }
      if (value !== modalInfo?.confirmText) {
        setErrorText(t('common.inputMismatch'));
        return false;
      }
      setErrorText('');
      return true;
    };

    return (
      <Modal
        title={modalInfo?.title}
        open={visible}
        maskClosable={false}
        onCancel={onCancel}
        okText={t('common.confirm')}
        cancelText={t('common.cancel')}
        okType="danger"
        okButtonProps={{ disabled: value !== modalInfo?.confirmText }}
        onOk={() => {
          if (!isSuccess()) {
            return false;
          }
          onClick(modalInfo?.id || '');
          onCancel();
        }}
      >
        <div className="typed-confirm-container">
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
            onFocus={() => setErrorText('')}
          />

          <p className="error-tip">{errorText}</p>
        </div>
      </Modal>
    );
  },
);

TypedConfirmModal.displayName = 'TypedConfirmModal';

export default TypedConfirmModal;
