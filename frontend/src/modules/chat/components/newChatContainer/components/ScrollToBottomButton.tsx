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
  return (
    <div
      style={{ bottom: inputHeight }}
      className={`toBottomContainer ${!visible ? "hidden" : ""}`}
    >
      <span className="toBottom" onClick={onClick}>
        <DoubleRightOutlined
          style={{
            fontSize: 18,
            cursor: "pointer",
            color: "#8d9ab2",
            transform: "rotate(90deg)",
          }}
        />
      </span>
    </div>
  );
}
