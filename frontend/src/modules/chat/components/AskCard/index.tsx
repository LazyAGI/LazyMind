import React, { useState } from 'react';
import { Button, Checkbox, Input, Radio } from 'antd';
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
}

interface AskCardProps {
  askPending: AskPending;
  /** Called with one formatted answer string per question, joined by newlines. */
  onSubmit: (formattedText: string) => void;
  disabled?: boolean;
}

const OTHER_OPTION = '其他';

type AnswerState =
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

function formatAnswer(q: AskQuestion, ans: AnswerState): string {
  switch (ans.type) {
    case 'boolean':
      return `${q.text}: ${ans.value ?? ''}`;
    case 'single': {
      const val = ans.value === OTHER_OPTION ? ans.otherText.trim() : (ans.value ?? '');
      return `${q.text}: ${val}`;
    }
    case 'multiple': {
      const parts = ans.value.map((v) =>
        v === OTHER_OPTION ? ans.otherText.trim() : v,
      );
      return `${q.text}: ${parts.join('、')}`;
    }
    case 'text':
      return `${q.text}: ${ans.value.trim()}`;
  }
}

export default function AskCard({ askPending, onSubmit, disabled = false }: AskCardProps) {
  const { t } = useTranslation();
  const { questions } = askPending;

  const [answers, setAnswers] = useState<AnswerState[]>(() =>
    questions.map(initAnswer),
  );

  const updateAnswer = (idx: number, next: AnswerState) => {
    setAnswers((prev) => prev.map((a, i) => (i === idx ? next : a)));
  };

  const canSubmit = answers.every(isAnswered);

  const handleSubmit = () => {
    if (disabled || !canSubmit) return;
    const lines = questions.map((q, i) => formatAnswer(q, answers[i]!));
    onSubmit(lines.join('\n'));
  };

  return (
    <div className={`ask-card${disabled ? ' ask-card--disabled' : ''}`} aria-label='Ask card'>
      {questions.map((q, idx) => {
        const ans = answers[idx]!;
        return (
          <div key={idx} className='ask-card__question-block'>
            <div className='ask-card__question'>{q.text}</div>
            {q.type === 'boolean' && (
              <div className='ask-card__boolean-buttons'>
                {(q.choices ?? ['是', '否']).map((c) => (
                  <Button
                    key={c}
                    size='small'
                    type={ans.type === 'boolean' && ans.value === c ? 'primary' : 'default'}
                    disabled={disabled}
                    onClick={() => updateAnswer(idx, { type: 'boolean', value: c })}
                    className='ask-card__bool-btn'
                  >
                    {c}
                  </Button>
                ))}
              </div>
            )}
            {q.type === 'single' && (
              <div className='ask-card__choices'>
                <Radio.Group
                  value={ans.type === 'single' ? ans.value : null}
                  onChange={(e) =>
                    updateAnswer(idx, { type: 'single', value: e.target.value, otherText: ans.type === 'single' ? ans.otherText : '' })
                  }
                  disabled={disabled}
                >
                  {(q.choices ?? []).map((c, ci) => (
                    <Radio key={ci} value={c} className='ask-card__choice'>
                      {c}
                    </Radio>
                  ))}
                </Radio.Group>
                {ans.type === 'single' && ans.value === OTHER_OPTION && (
                  <Input
                    size='small'
                    value={ans.otherText}
                    onChange={(e) =>
                      updateAnswer(idx, { type: 'single', value: OTHER_OPTION, otherText: e.target.value })
                    }
                    disabled={disabled}
                    placeholder={t('chat.askCardOtherPlaceholder')}
                    className='ask-card__other-input'
                  />
                )}
              </div>
            )}
            {q.type === 'multiple' && (
              <div className='ask-card__choices'>
                <Checkbox.Group
                  value={ans.type === 'multiple' ? ans.value : []}
                  onChange={(vals) =>
                    updateAnswer(idx, {
                      type: 'multiple',
                      value: vals as string[],
                      otherText: ans.type === 'multiple' ? ans.otherText : '',
                    })
                  }
                  disabled={disabled}
                >
                  {(q.choices ?? []).map((c, ci) => (
                    <Checkbox key={ci} value={c} className='ask-card__choice'>
                      {c}
                    </Checkbox>
                  ))}
                </Checkbox.Group>
                {ans.type === 'multiple' && ans.value.includes(OTHER_OPTION) && (
                  <Input
                    size='small'
                    value={ans.otherText}
                    onChange={(e) =>
                      updateAnswer(idx, {
                        type: 'multiple',
                        value: ans.value,
                        otherText: e.target.value,
                      })
                    }
                    disabled={disabled}
                    placeholder={t('chat.askCardOtherPlaceholder')}
                    className='ask-card__other-input'
                  />
                )}
              </div>
            )}
            {q.type === 'text' && (
              <Input.TextArea
                value={ans.type === 'text' ? ans.value : ''}
                onChange={(e) => updateAnswer(idx, { type: 'text', value: e.target.value })}
                disabled={disabled}
                placeholder={t('chat.askCardInputPlaceholder')}
                className='ask-card__input'
                autoSize={{ minRows: 1, maxRows: 4 }}
              />
            )}
          </div>
        );
      })}
      {!disabled && (
        <Button
          type='primary'
          size='small'
          disabled={!canSubmit}
          onClick={handleSubmit}
          className='ask-card__submit'
        >
          {t('chat.askCardSubmit')}
        </Button>
      )}
    </div>
  );
}
