import React, { useState } from 'react';
import { Button, Checkbox, Input, Radio } from 'antd';
import { useTranslation } from 'react-i18next';
import './index.scss';

export interface AskPending {
  ask_id: string;
  question: string;
  choices?: string[];
  allow_multiple?: boolean;
}

interface AskCardProps {
  askPending: AskPending;
  onSubmit: (selected: string[]) => void;
  disabled?: boolean;
}

export default function AskCard({ askPending, onSubmit, disabled = false }: AskCardProps) {
  const { t } = useTranslation();
  const { question, choices, allow_multiple } = askPending;
  const isChoiceMode = Array.isArray(choices) && choices.length > 0;

  const [selectedMultiple, setSelectedMultiple] = useState<string[]>([]);
  const [selectedSingle, setSelectedSingle] = useState<string | undefined>(undefined);
  const [freeText, setFreeText] = useState('');

  const handleSubmit = () => {
    if (disabled) return;
    if (isChoiceMode) {
      const result = allow_multiple ? selectedMultiple : (selectedSingle ? [selectedSingle] : []);
      onSubmit(result);
    } else {
      onSubmit(freeText.trim() ? [freeText.trim()] : []);
    }
  };

  const canSubmit = isChoiceMode
    ? allow_multiple ? selectedMultiple.length > 0 : !!selectedSingle
    : freeText.trim().length > 0;

  return (
    <div className={`ask-card${disabled ? ' ask-card--disabled' : ''}`} aria-label='Ask card'>
      <div className='ask-card__question'>{question}</div>
      {isChoiceMode ? (
        <div className='ask-card__choices'>
          {allow_multiple ? (
            <Checkbox.Group
              value={selectedMultiple}
              onChange={(vals) => setSelectedMultiple(vals as string[])}
              disabled={disabled}
            >
              {choices!.map((c, i) => (
                <Checkbox key={i} value={c} className='ask-card__choice'>
                  {c}
                </Checkbox>
              ))}
            </Checkbox.Group>
          ) : (
            <Radio.Group
              value={selectedSingle}
              onChange={(e) => setSelectedSingle(e.target.value)}
              disabled={disabled}
            >
              {choices!.map((c, i) => (
                <Radio key={i} value={c} className='ask-card__choice'>
                  {c}
                </Radio>
              ))}
            </Radio.Group>
          )}
        </div>
      ) : (
        <Input
          value={freeText}
          onChange={(e) => setFreeText(e.target.value)}
          disabled={disabled}
          placeholder={t('chat.askCardInputPlaceholder')}
          className='ask-card__input'
          onPressEnter={handleSubmit}
        />
      )}
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
