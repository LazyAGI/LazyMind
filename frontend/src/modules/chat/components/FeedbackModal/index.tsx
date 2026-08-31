import { useState, useEffect, type ChangeEvent } from "react";
import { Modal, Button, Input, Space, message } from "antd";
import { CloseOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import "./index.scss";

const { TextArea } = Input;

interface FeedbackModalProps {
  visible: boolean;
  onCancel: () => void;
  onSubmit: (reason: string[], comment: string) => void;
  initialReason?: string;
  initialComment?: string;
  submitLoading?: boolean;
}

const FEEDBACK_OPTION_IDS = [
  "didNotUnderstand",
  "didNotCompleteTask",
  "fabricatedFacts",
  "tooVerbose",
  "notCreative",
  "poorWritingStyle",
  "outdatedInfo",
  "other",
] as const;

const FeedbackModal = ({
  visible,
  onCancel,
  onSubmit,
  initialReason = "",
  initialComment = "",
  submitLoading = false,
}: FeedbackModalProps) => {
  const { t } = useTranslation();
  const feedbackOptions = FEEDBACK_OPTION_IDS.map((id) => ({
    id,
    label: t(`chatFeedback.${id}`),
  }));
  const [selectedReasons, setSelectedReasons] = useState<string[]>([]);
  const [comment, setComment] = useState("");
  const otherReason = t("chatFeedback.other");
  const effectiveSelectedReasons =
    comment.trim() && !selectedReasons.includes(otherReason)
      ? [...selectedReasons, otherReason]
      : selectedReasons;

  useEffect(() => {
    if (visible) {
      setSelectedReasons(
        initialReason.split(",").map((value) => value.trim()).filter(Boolean),
      );
      setComment(initialComment);
      return;
    }
    setSelectedReasons([]);
    setComment("");
  }, [initialComment, initialReason, visible]);

  const handleReasonClick = (value: string) => {
    if (selectedReasons.includes(value)) {
      setSelectedReasons(selectedReasons.filter((r) => r !== value));
    } else {
      setSelectedReasons([...selectedReasons, value]);
    }
  };

  const handleSubmit = () => {
    if (effectiveSelectedReasons.length === 0) {
      message.error(t("chat.atLeastOneUnsatisfiedReason"));
      return;
    }
    if (submitLoading) {
      return;
    }
    onSubmit(effectiveSelectedReasons, comment);
  };

  const handleCancel = () => {
    setSelectedReasons([]);
    setComment("");
    onCancel();
  };

  return (
    <Modal
      open={visible}
      onCancel={handleCancel}
      footer={null}
      closeIcon={<CloseOutlined />}
      width={720}
      className="feedback-modal"
    >
      <div className="feedback-modal-content">
        <h3 className="feedback-title">{t("chat.feedbackAskUnsatisfied")}</h3>
        <p className="feedback-subtitle">{t("chat.feedbackSubtitle")}</p>
        <Space wrap className="feedback-options">
          {feedbackOptions.map(({ id, label }) => (
            <Button
              key={id}
              type={effectiveSelectedReasons.includes(label) ? "primary" : "default"}
              onClick={() => handleReasonClick(label)}
              className="feedback-option-btn"
            >
              {label}
            </Button>
          ))}
        </Space>

        <div className="feedback-comment">
          <TextArea
            placeholder={t("chat.expectedAnswer")}
            value={comment}
            onChange={(event: ChangeEvent<HTMLTextAreaElement>) => (
              setComment(event.target.value)
            )}
            rows={6}
            maxLength={200}
            showCount={{
              formatter: ({ count, maxLength }: { count: number; maxLength?: number }) => (
                `${count}/${maxLength ?? 200}`
              ),
            }}
          />
        </div>

        <div className="feedback-actions">
          <Button onClick={handleCancel} disabled={submitLoading}>
            {t("common.cancel")}
          </Button>
          <Button
            type="primary"
            onClick={handleSubmit}
            loading={submitLoading}
            disabled={submitLoading}
          >
            {t("chat.submitFeedback")}
          </Button>
        </div>
      </div>
    </Modal>
  );
};

export default FeedbackModal;
