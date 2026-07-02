import React, { useRef, useState } from 'react';
import { Button, Checkbox, Input, Progress, Radio } from 'antd';
import { EditOutlined, LeftOutlined, RightOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import './index.scss';

export interface AskQuestion {
  text: string;
  type: 'boolean' | 'single' | 'multiple' | 'text';
  choices?: string[];
}

export interface AskPending {
  ask_id: string;
  questions: AskQuestion[];
  /** Optional group title shown at the top of the card */
  title?: string;
  /** Optional subtitle / description shown below the title */
  description?: string;
}

export interface AskAnsweredQuestion {
  text: string;
  type: string;
  choices: string[];        // original choice list (from AskQuestion.choices)
  custom_choices: string[]; // user-edited choice labels
  answer: AnswerState | null;
}

export interface AskAnswersStructured {
  ask_id: string;
  questions: AskAnsweredQuestion[];
}

export interface AskSubmitPayload {
  /** Formatted text for display in the user message bubble. */
  text: string;
  /** Full structured answers, forwarded to the backend as ask_answers_structured. */
  structured: AskAnswersStructured;
}

interface AskCardProps {
  askPending: AskPending;
  /** Called with a payload containing the formatted text and full structured answers. */
  onSubmit: (payload: AskSubmitPayload) => void;
  disabled?: boolean;
  /** Cached answers to pre-populate (index → serialized answer) */
  savedAnswers?: Record<number, AnswerState>;
  /** Called whenever an answer changes, for external caching */
  onAnswerChange?: (index: number, ans: AnswerState) => void;
}

const OTHER_OPTION = '其他';

export type AnswerState =
  | { type: 'boolean'; value: string | null }
  | { type: 'single'; value: string | null; otherText: string }
  | { type: 'multiple'; value: string[]; otherText: string }
  | { type: 'text'; value: string };

function initAnswer(q: AskQuestion): AnswerState {
  switch (q.type) {
    case 'boolean':
      return { type: 'boolean', value: null };
    case 'single':
      return { type: 'single', value: null, otherText: '' };
    case 'multiple':
      return { type: 'multiple', value: [], otherText: '' };
    default:
      return { type: 'text', value: '' };
  }
}

function isAnswered(ans: AnswerState): boolean {
  switch (ans.type) {
    case 'boolean':
      return ans.value !== null;
    case 'single':
      if (!ans.value) return false;
      return ans.value !== OTHER_OPTION || ans.otherText.trim().length > 0;
    case 'multiple':
      if (ans.value.length === 0) return false;
      if (ans.value.includes(OTHER_OPTION)) return ans.otherText.trim().length > 0;
      return true;
    case 'text':
      return ans.value.trim().length > 0;
  }
}

function formatAnswer(q: AskQuestion, ans: AnswerState, choices: string[]): string {
  switch (ans.type) {
    case 'boolean':
      return `${q.text}: ${ans.value ?? ''}`;
    case 'single': {
      const raw = ans.value ?? '';
      // Resolve the original choice index to get the (possibly edited) label.
      const origChoices = q.choices ?? [];
      const origIdx = origChoices.indexOf(raw);
      const label = origIdx >= 0 ? (choices[origIdx] ?? raw) : raw;
      const val = raw === OTHER_OPTION ? ans.otherText.trim() : label;
      return `${q.text}: ${val}`;
    }
    case 'multiple': {
      const origChoices = q.choices ?? [];
      const parts = ans.value.map((v) => {
        if (v === OTHER_OPTION) return ans.otherText.trim();
        const origIdx = origChoices.indexOf(v);
        return origIdx >= 0 ? (choices[origIdx] ?? v) : v;
      });
      return `${q.text}: ${parts.join('、')}`;
    }
    case 'text':
      return `${q.text}: ${ans.value.trim()}`;
  }
}

/** Editable inline choice label. */
function EditableChoice({
  value,
  disabled,
  onChange,
}: {
  value: string;
  disabled: boolean;
  onChange: (next: string) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);
  const inputRef = useRef<any>(null);

  const startEdit = (e: React.MouseEvent) => {
    if (disabled) return;
    e.stopPropagation();
    setDraft(value);
    setEditing(true);
    // Focus after render
    setTimeout(() => inputRef.current?.focus(), 0);
  };

  const commit = () => {
    setEditing(false);
    const trimmed = draft.trim();
    if (trimmed && trimmed !== value) {
      onChange(trimmed);
    } else {
      setDraft(value);
    }
  };

  if (editing) {
    return (
      <Input
        ref={inputRef}
        size='small'
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onPressEnter={commit}
        onClick={(e) => e.stopPropagation()}
        className='ask-wizard__choice-input'
      />
    );
  }

  return (
    <span className='ask-wizard__choice-label'>
      {value}
      {!disabled && (
        <EditOutlined
          className='ask-wizard__choice-edit-icon'
          onClick={startEdit}
          title='Edit option'
        />
      )}
    </span>
  );
}

export default function AskCard({
  askPending,
  onSubmit,
  disabled = false,
  savedAnswers,
  onAnswerChange,
}: AskCardProps) {
  const { t } = useTranslation();
  const { questions, title, description } = askPending;
  const total = questions.length;

  const [answers, setAnswers] = useState<AnswerState[]>(() =>
    questions.map((q, i) => savedAnswers?.[i] ?? initAnswer(q)),
  );
  const [currentIndex, setCurrentIndex] = useState(0);

  // Per-question, per-choice edited labels. Keyed as `${qIdx}-${choiceIdx}`.
  // Stored as Record<qIdx, string[]> mirroring the original choices array.
  const [customChoices, setCustomChoices] = useState<Record<number, string[]>>(() =>
    Object.fromEntries(questions.map((q, i) => [i, [...(q.choices ?? [])]])),
  );

  const currentQ = questions[currentIndex]!;
  const currentAns = answers[currentIndex]!;
  const currentAnswered = isAnswered(currentAns);
  const allAnswered = answers.every(isAnswered);
  const currentChoices = customChoices[currentIndex] ?? (currentQ.choices ?? []);

  const progressPercent = Math.round((answers.filter(isAnswered).length / total) * 100);

  const updateAnswer = (idx: number, next: AnswerState, autoAdvance = false) => {
    setAnswers((prev) => {
      const updated = prev.map((a, i) => (i === idx ? next : a));
      onAnswerChange?.(idx, next);
      return updated;
    });
    if (autoAdvance && idx < total - 1) {
      setTimeout(() => setCurrentIndex(idx + 1), 180);
    }
  };

  const updateChoice = (qIdx: number, choiceIdx: number, newLabel: string) => {
    setCustomChoices((prev) => {
      const updated = { ...prev };
      const arr = [...(updated[qIdx] ?? questions[qIdx]!.choices ?? [])];
      arr[choiceIdx] = newLabel;
      updated[qIdx] = arr;
      return updated;
    });
  };

  const handleSubmit = () => {
    if (disabled || !allAnswered) return;
    const lines = questions.map((q, i) =>
      formatAnswer(q, answers[i]!, customChoices[i] ?? q.choices ?? []),
    );
    const structured: AskAnswersStructured = {
      ask_id: askPending.ask_id,
      questions: questions.map((q, i) => ({
        text: q.text,
        type: q.type,
        choices: q.choices ?? [],
        custom_choices: customChoices[i] ?? q.choices ?? [],
        answer: answers[i] ?? null,
      })),
    };
    onSubmit({ text: lines.join('\n'), structured });
  };

  const goTo = (idx: number) => {
    if (idx >= 0 && idx < total) setCurrentIndex(idx);
  };

  const canGoNext = currentIndex < total - 1;
  const canGoPrev = currentIndex > 0;

  return (
    <div className={`ask-wizard${disabled ? ' ask-wizard--disabled' : ''}`} aria-label='Ask card'>
      {/* Header */}
      <div className='ask-wizard__header'>
        <div className='ask-wizard__header-top'>
          <div className='ask-wizard__title-area'>
            {title && <h3 className='ask-wizard__title'>{title}</h3>}
            {description && <p className='ask-wizard__description'>{description}</p>}
          </div>
          <div className='ask-wizard__meta'>
            <span className='ask-wizard__count'>
              {currentIndex + 1} / {total}
            </span>
          </div>
        </div>
        <Progress
          percent={progressPercent}
          showInfo={false}
          size={['100%', 3]}
          className='ask-wizard__progress'
          strokeColor='#4e6ef2'
          trailColor='#e4e9f5'
        />
        <div className='ask-wizard__progress-label'>
          {progressPercent}% {t('chat.askCardCompleted')}
        </div>
      </div>

      {/* Question body */}
      <div className='ask-wizard__body'>
        <div className='ask-wizard__question-label'>
          <span className='ask-wizard__index-badge'>{currentIndex + 1}</span>
          <span className='ask-wizard__question-text'>{currentQ.text}</span>
        </div>

        <div className='ask-wizard__answer-area'>
          {currentQ.type === 'boolean' && (
            <div className='ask-wizard__boolean-buttons'>
              {(currentChoices.length > 0 ? currentChoices : ['是', '否']).map((c, ci) => (
                <Button
                  key={ci}
                  type={currentAns.type === 'boolean' && currentAns.value === (currentQ.choices?.[ci] ?? c) ? 'primary' : 'default'}
                  disabled={disabled}
                  onClick={() => updateAnswer(
                    currentIndex,
                    { type: 'boolean', value: currentQ.choices?.[ci] ?? c },
                    true,
                  )}
                  className='ask-wizard__bool-btn'
                >
                  {c}
                </Button>
              ))}
            </div>
          )}

          {currentQ.type === 'single' && (
            <div className='ask-wizard__choices'>
              <Radio.Group
                value={currentAns.type === 'single' ? currentAns.value : null}
                onChange={(e) => {
                  const chosen = e.target.value as string;
                  updateAnswer(
                    currentIndex,
                    { type: 'single', value: chosen, otherText: currentAns.type === 'single' ? currentAns.otherText : '' },
                    chosen !== OTHER_OPTION,
                  );
                }}
                disabled={disabled}
              >
                {(currentQ.choices ?? []).map((origVal, ci) => (
                  <Radio key={ci} value={origVal} className='ask-wizard__choice'>
                    <EditableChoice
                      value={currentChoices[ci] ?? origVal}
                      disabled={disabled}
                      onChange={(next) => updateChoice(currentIndex, ci, next)}
                    />
                  </Radio>
                ))}
              </Radio.Group>
              {currentAns.type === 'single' && currentAns.value === OTHER_OPTION && (
                <Input
                  value={currentAns.otherText}
                  onChange={(e) =>
                    updateAnswer(currentIndex, { type: 'single', value: OTHER_OPTION, otherText: e.target.value })
                  }
                  disabled={disabled}
                  placeholder={t('chat.askCardOtherPlaceholder')}
                  className='ask-wizard__other-input'
                />
              )}
            </div>
          )}

          {currentQ.type === 'multiple' && (
            <div className='ask-wizard__choices'>
              <Checkbox.Group
                value={currentAns.type === 'multiple' ? currentAns.value : []}
                onChange={(vals) =>
                  updateAnswer(currentIndex, {
                    type: 'multiple',
                    value: vals as string[],
                    otherText: currentAns.type === 'multiple' ? currentAns.otherText : '',
                  })
                }
                disabled={disabled}
              >
                {(currentQ.choices ?? []).map((origVal, ci) => (
                  <Checkbox key={ci} value={origVal} className='ask-wizard__choice'>
                    <EditableChoice
                      value={currentChoices[ci] ?? origVal}
                      disabled={disabled}
                      onChange={(next) => updateChoice(currentIndex, ci, next)}
                    />
                  </Checkbox>
                ))}
              </Checkbox.Group>
              {currentAns.type === 'multiple' && currentAns.value.includes(OTHER_OPTION) && (
                <Input
                  value={currentAns.type === 'multiple' ? currentAns.otherText : ''}
                  onChange={(e) =>
                    updateAnswer(currentIndex, {
                      type: 'multiple',
                      value: currentAns.type === 'multiple' ? currentAns.value : [],
                      otherText: e.target.value,
                    })
                  }
                  disabled={disabled}
                  placeholder={t('chat.askCardOtherPlaceholder')}
                  className='ask-wizard__other-input'
                />
              )}
            </div>
          )}

          {currentQ.type === 'text' && (
            <Input.TextArea
              value={currentAns.type === 'text' ? currentAns.value : ''}
              onChange={(e) => updateAnswer(currentIndex, { type: 'text', value: e.target.value })}
              disabled={disabled}
              placeholder={t('chat.askCardInputPlaceholder')}
              className='ask-wizard__text-input'
              autoSize={{ minRows: 2, maxRows: 5 }}
            />
          )}
        </div>
      </div>

      {/* Navigation + quick jump */}
      <div className='ask-wizard__footer'>
        <div className='ask-wizard__nav-buttons'>
          <Button
            icon={<LeftOutlined />}
            disabled={!canGoPrev || disabled}
            onClick={() => goTo(currentIndex - 1)}
            className='ask-wizard__nav-btn'
          >
            {t('chat.askCardPrev')}
          </Button>
          {canGoNext ? (
            <Button
              type='primary'
              onClick={() => goTo(currentIndex + 1)}
              disabled={disabled}
              className='ask-wizard__nav-btn'
            >
              {t('chat.askCardNext')}
              <RightOutlined />
            </Button>
          ) : (
            !disabled && (
              <Button
                type='primary'
                disabled={!allAnswered}
                onClick={handleSubmit}
                className='ask-wizard__submit-btn'
              >
                {t('chat.askCardSubmit')}
              </Button>
            )
          )}
        </div>

        {/* Quick-jump sidebar */}
        <div className='ask-wizard__jump-list'>
          {questions.map((_, idx) => (
            <button
              key={idx}
              type='button'
              className={`ask-wizard__jump-item${idx === currentIndex ? ' is-current' : ''}${isAnswered(answers[idx]!) ? ' is-done' : ''}`}
              onClick={() => goTo(idx)}
              disabled={disabled}
              aria-label={`Go to question ${idx + 1}`}
            >
              {idx + 1}
            </button>
          ))}
        </div>
      </div>

      {!disabled && (
        <p className='ask-wizard__hint'>{t('chat.askCardAutoSaveHint')}</p>
      )}
    </div>
  );
}
