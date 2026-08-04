import { useEffect, useState } from 'react';
import { Button, Checkbox, Modal, message } from 'antd';
import { useTranslation } from 'react-i18next';
import { useLocation, useNavigate } from 'react-router-dom';
import LegalLanguageToggle from '@/legal/LegalLanguageToggle';
import logoImage from '@/public/Lazy.png';
import {
  consumeUserAgreementReadFlag,
  persistUserAgreementAccepted,
  syncUserAgreementFromServer,
  USER_AGREEMENT_VERSION,
} from '@/legal/consent';
import './index.scss';

interface UserAgreementConsentModalProps {
  open: boolean;
  onAccepted: () => void;
}

export default function UserAgreementConsentModal({
  open,
  onAccepted,
}: UserAgreementConsentModalProps) {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const [submitting, setSubmitting] = useState(false);
  const [confirmed, setConfirmed] = useState(consumeUserAgreementReadFlag);

  const handleAgree = async () => {
    setSubmitting(true);
    try {
      await persistUserAgreementAccepted();
      onAccepted();
    } catch (error) {
      console.error('Failed to persist user agreement:', error);
      message.error(t('legal.consentPersistFailed'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      className="user-agreement-consent-modal"
      title={null}
      open={open}
      width={520}
      centered
      closable={false}
      keyboard={false}
      maskClosable={false}
      destroyOnHidden
      footer={null}
    >
      <div className="user-agreement-consent-hero">
        <div className="user-agreement-consent-language">
          <LegalLanguageToggle />
        </div>
        <img
          className="user-agreement-consent-logo"
          src={logoImage}
          alt="LazyMind"
        />
        <h1>{t('legal.welcomeTitle')}</h1>
        <p>{t('legal.welcomeDescription')}</p>
      </div>

      <div className="user-agreement-consent-checkbox">
        <Checkbox
          checked={confirmed}
          aria-labelledby="user-agreement-consent-label"
          onChange={(event) => setConfirmed(event.target.checked)}
        />
        <span id="user-agreement-consent-label">
          {t('legal.readAndAgreePrefix')}
          <button
            type="button"
            className="user-agreement-consent-link"
            onClick={(event) => {
              event.preventDefault();
              event.stopPropagation();
              navigate('/legal/user-agreement', {
                state: { from: location.pathname + location.search },
              });
            }}
          >
            {t('legal.agreementLink')}
          </button>
        </span>
      </div>

      <div className="user-agreement-consent-actions">
        <Button
          type="primary"
          size="large"
          block
          disabled={!confirmed}
          loading={submitting}
          onClick={handleAgree}
        >
          {t('legal.consentAgreeAndContinue')}
        </Button>
        <span>
          {t('legal.consentVersion', { version: USER_AGREEMENT_VERSION })}
        </span>
      </div>
    </Modal>
  );
}

export function useUserAgreementConsentGate(enabled: boolean) {
  const [loading, setLoading] = useState(enabled);
  const [accepted, setAccepted] = useState(false);
  const [checkFailed, setCheckFailed] = useState(false);
  const [checkAttempt, setCheckAttempt] = useState(0);

  useEffect(() => {
    if (!enabled) {
      setLoading(false);
      setAccepted(true);
      setCheckFailed(false);
      return;
    }

    let cancelled = false;
    setLoading(true);
    setCheckFailed(false);
    void syncUserAgreementFromServer()
      .then((ok) => {
        if (!cancelled) {
          setAccepted(ok);
        }
      })
      .catch((error) => {
        console.error('Failed to sync user agreement from server:', error);
        if (!cancelled) {
          setCheckFailed(true);
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [enabled, checkAttempt]);

  return {
    loading,
    checkFailed,
    needsConsent: enabled && !loading && !checkFailed && !accepted,
    retryCheck: () => setCheckAttempt((attempt) => attempt + 1),
    markAccepted: () => {
      setAccepted(true);
      setCheckFailed(false);
    },
  };
}
