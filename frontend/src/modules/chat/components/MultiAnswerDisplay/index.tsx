import { useState, useEffect } from "react";
import { Space, Radio } from "antd";
import type { RadioChangeEvent } from "antd";
import { useTranslation } from "react-i18next";
import "./index.scss";

export interface Answer {
  content: string;
  index: number;
  history_id?: string;
  reasoning_content?: string;
  sources?: any[];
  thinking_duration_s?: string;
}

export type PreferenceType =
  | "prefer_first"
  | "prefer_second"
  | "similar"
  | "neither";

interface MultiAnswerDisplayProps {
  answers: Answer[];
  renderText: (
    content: string,
    reasoningContent?: string,
    answerIndex?: number,
  ) => React.ReactNode;
  onSelectAnswer?: (selectedIndex: number, preference: PreferenceType) => void;
  disabled?: boolean;
  renderFooter?: (
    answerIndex: number,
    showFullToolbar: boolean,
  ) => React.ReactNode;
  renderSources?: (answerIndex: number) => React.ReactNode;
  initialSelectedIndex?: number;
  initialPreference?: PreferenceType;
  isStreaming?: boolean;
  showPreference?: boolean;
}

const MultiAnswerDisplay: React.FC<MultiAnswerDisplayProps> = ({
  answers,
  renderText,
  onSelectAnswer,
  disabled = false,
  renderFooter,
  renderSources,
  initialSelectedIndex,
  initialPreference,
  isStreaming = false,
  showPreference = true,
}) => {
  const { t } = useTranslation();
  const [selectedAnswer, setSelectedAnswer] = useState<number | null>(
    initialSelectedIndex ?? null,
  );
  const [preference, setPreference] = useState<PreferenceType | null>(
    initialPreference ?? null,
  );

  useEffect(() => {
    if (initialSelectedIndex !== undefined) {
      setSelectedAnswer(initialSelectedIndex);
    }
  }, [initialSelectedIndex]);

  useEffect(() => {
    if (initialPreference !== undefined) {
      setPreference(initialPreference);
    }
  }, [initialPreference]);

  if (!answers || answers.length < 2) {
    return null;
  }

  const handlePreferenceChange = (e: RadioChangeEvent) => {
    const newPreference = e.target.value;
    setPreference(newPreference);

    if (newPreference === "prefer_first") {
      setSelectedAnswer(0);
      onSelectAnswer?.(0, newPreference);
    } else if (newPreference === "prefer_second") {
      setSelectedAnswer(1);
      onSelectAnswer?.(1, newPreference);
    } else if (newPreference === "similar" || newPreference === "neither") {
      setSelectedAnswer(0);
      onSelectAnswer?.(0, newPreference);
    }
  };

  if (selectedAnswer !== null) {
    const selectedAnswerData = answers[selectedAnswer];
    return (
      <div className="multi-answer-container">
        {}
        <div className="selected-answer">
          <div className="answer-content">
            {renderText(
              selectedAnswerData.content,
              selectedAnswerData.reasoning_content,
              selectedAnswer,
            )}
          </div>

          {}
          {renderSources && renderSources(selectedAnswer)}

          {}
          {renderFooter && renderFooter(selectedAnswer, true)}
        </div>
      </div>
    );
  }
  return (
    <div className="multi-answer-container">
      {}
      <div className={`answers-wrapper ${isStreaming ? "streaming" : ""}`}>
        {answers.map((answer, index) => (
          <div
            key={answer.history_id || index}
            className={`answer-item ${selectedAnswer === index ? "selected" : ""}`}
          >
            <div className="answer-header">
              <span className="answer-label">
                {index === 0 ? t("chat.lazyMindModel") : "DeepSeek"}
              </span>
            </div>
            <div className="answer-content">
              {}
              {renderText(answer.content, answer.reasoning_content, index)}
            </div>

            {}
            {renderSources && index === 0 && renderSources(index)}

            {}
            {renderFooter && renderFooter(index, false)}
          </div>
        ))}
      </div>

      {}
      {!disabled && showPreference && (
        <div className="preference-section-bottom">
          <div className="preference-title">{t("chat.preferredAnswerVersion")}</div>
          <Radio.Group
            onChange={handlePreferenceChange}
            value={preference}
            buttonStyle="solid"
          >
            <Space size="middle">
              <Radio.Button value="prefer_first">{t("chat.lazyMindModel")}</Radio.Button>
              <Radio.Button value="prefer_second">DeepSeek</Radio.Button>
              <Radio.Button value="similar">{t("chat.bothGood")}</Radio.Button>
              <Radio.Button value="neither">{t("chat.bothBad")}</Radio.Button>
            </Space>
          </Radio.Group>
        </div>
      )}
    </div>
  );
};

export default MultiAnswerDisplay;
