import { Button } from 'antd';
import { ArrowLeftOutlined } from '@ant-design/icons';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { useTranslation } from 'react-i18next';
import { useLocation, useNavigate } from 'react-router-dom';
import LegalLanguageToggle from '@/legal/LegalLanguageToggle';
import { getUserAgreementMarkdown } from '@/legal/agreementContent';
import {
  markUserAgreementRead,
  USER_AGREEMENT_VERSION,
} from '@/legal/consent';
import logoImage from '@/public/Lazy.png';
import './index.scss';

interface AgreementLocationState {
  from?: string;
}

export default function UserAgreementPage() {
  const { t, i18n } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const requestedFrom = (location.state as AgreementLocationState | null)?.from;
  const from =
    requestedFrom?.startsWith('/') &&
    !requestedFrom.startsWith('//') &&
    requestedFrom !== '/legal/user-agreement'
      ? requestedFrom
      : '/';
  const agreementMarkdown = getUserAgreementMarkdown(i18n.language);

  const handleReadAndReturn = () => {
    markUserAgreementRead();
    navigate(from, { replace: true });
  };

  return (
    <div className="user-agreement-page">
      <header className="user-agreement-page-header">
        <Button
          type="text"
          icon={<ArrowLeftOutlined />}
          onClick={() => navigate(from, { replace: true })}
        >
          {t('legal.detailsBack')}
        </Button>
        <img src={logoImage} alt="LazyMind" />
        <div className="user-agreement-page-language">
          <LegalLanguageToggle />
        </div>
      </header>

      <main className="user-agreement-page-main">
        <div className="user-agreement-page-title">
          <h1>{t('legal.consentTitle')}</h1>
          <p>{t('legal.consentVersion', { version: USER_AGREEMENT_VERSION })}</p>
        </div>

        <article className="user-agreement-page-content">
          <Markdown remarkPlugins={[[remarkGfm, { singleTilde: false }]]}>
            {agreementMarkdown}
          </Markdown>
        </article>
      </main>

      <footer className="user-agreement-page-footer">
        <Button type="primary" size="large" onClick={handleReadAndReturn}>
          {t('legal.detailsReadAndReturn')}
        </Button>
      </footer>
    </div>
  );
}
