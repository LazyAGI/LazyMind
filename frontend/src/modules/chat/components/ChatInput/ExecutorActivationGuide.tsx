import { Modal, Select } from 'antd';
import { CloudDownloadOutlined, ExportOutlined, LinkOutlined, LoginOutlined, PoweroffOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { AGENTS, agentIntegrationHref } from '@/modules/agentIntegration/catalog';

type Agent = typeof AGENTS[number];

/** Render the CLI commands from the settings guide as copyable inline text. */
function instructions(text: string) {
  return text.split(/(`[^`]+`)/).map((part, i) => part.startsWith('`') ? <code key={i}>{part.slice(1, -1)}</code> : part);
}

export default function ExecutorActivationGuide({ agent, onSelect, onClose }: {
  agent: Agent;
  onSelect: (agent: Agent) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const dsh = agent.id === 'deepseek-harness';
  const steps = [
    { name: 'install', icon: <CloudDownloadOutlined />, label: t('chat.executorGuide.installTitle') },
    { name: 'login', icon: dsh ? <LinkOutlined /> : <LoginOutlined />, label: t(dsh ? 'chat.executorGuide.connectTitle' : 'chat.executorGuide.loginTitle') },
    { name: 'enable', icon: <PoweroffOutlined />, label: t('chat.executorGuide.enableTitle') },
  ];

  return <Modal open onCancel={onClose} width={500} title={t('chat.executorGuide.title')}
    footer={<Link to={agentIntegrationHref(agent.id)} target="_blank" rel="noopener noreferrer"
      className="chat-config-guide-settings" onClick={onClose}>
      {t('chat.executorGuide.openSettings', { name: agent.name })}<ExportOutlined aria-hidden />
    </Link>}>
    <div className="chat-config-guide-heading">
      <img src={agent.icon} alt="" />
      <Select<Agent['id']> aria-label={t('chat.conversationConfigExecutor')} value={agent.id}
        options={AGENTS.map(item => ({ value: item.id, label: item.name }))}
        onChange={(id: Agent['id']) => { const next = AGENTS.find(item => item.id === id); if (next) onSelect(next); }} />
    </div>
    {agent.executorName ? <ol className="chat-config-guide-steps">
      {steps.map((step, index) => <li key={step.name}>
        <span className="chat-config-guide-illustration" aria-hidden>{step.icon}</span>
        <div><strong>{index + 1}. {step.label}</strong>
          <p>{instructions(t(`agentIntegration.guides.${agent.id}.executor.${step.name}`))}</p>
          {dsh && step.name === 'login' && <img className="chat-config-guide-screenshot"
            src="/installation-guides/deepseek-harness/enable.png" alt={t('agentIntegration.dshInstallGuide.imageAlt')} />}
          {step.name === 'enable' && <figure className="chat-config-guide-switch">
            <img src={agent.icon} alt="" /><figcaption>{t('agentIntegration.executorModeTitle', { agent: agent.executorName })}</figcaption>
            <span aria-hidden />
          </figure>}
        </div>
      </li>)}
    </ol> : <p className="chat-config-guide-inbound">{t('chat.executorGuide.inboundDescription', { name: agent.name })}</p>}
  </Modal>;
}
