import { useTranslation } from "react-i18next";
import { DoubleRightOutlined } from "@ant-design/icons";

interface ScrollToBottomButtonProps {
  visible: boolean;
  inputHeight: number;
  onClick: () => void;
}

export default function ScrollToBottomButton({
  visible,
  inputHeight,
  onClick,
}: ScrollToBottomButtonProps) {
  const { t } = useTranslation();
  return (
    <div
      style={{ bottom: inputHeight }}
      aria-hidden={!visible}
      className={`toBottomContainer ${!visible ? "hidden" : ""}`}
    >
      <button type="button" className="toBottom" onClick={onClick} aria-label={t("chat.scrollToLatest")} tabIndex={visible ? 0 : -1}>
        <DoubleRightOutlined
          style={{
            fontSize: 18,
            cursor: "pointer",
            color: "#8d9ab2",
            transform: "rotate(90deg)",
          }}
        />
      </button>
    </div>
  );
}
