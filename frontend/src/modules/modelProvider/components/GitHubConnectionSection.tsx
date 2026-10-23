import { Alert, Button, Modal, Space, Switch, Tag, Typography } from "antd";
import { useRef, type ComponentRef, type ReactNode } from "react";
import type { CloudDocumentProvidersVm } from "../hooks/useCloudDocumentProviders";
import "./githubConnectionSection.scss";

export default function GitHubConnectionSection({ vm, logo }: { vm: CloudDocumentProvidersVm; logo: ReactNode }) {
  const { t, github } = vm;
  const trigger = useRef<HTMLElement | null>(null);
  const connectButton = useRef<ComponentRef<typeof Button>>(null);
  const label = (key: string) => t(`modelProvider.cloudDocuments.githubCli.${key}`);
  const waiting = github.session?.status === "WAITING_USER";
  const accountName = (name: string, id: string) => name || id;

  return <section className="github-connection-section" aria-labelledby="github-connections-title">
    <div className="github-connection-header">
      {logo}
      <div className="model-provider-cloud-doc-resource-copy">
        <h3 id="github-connections-title">GitHub</h3>
        <p>{label("description")}</p>
      </div>
      <Button ref={connectButton} type="link" disabled={github.active || github.retryCreate}
        onClick={event => { trigger.current = event.currentTarget; void github.start(); }}>
        {label("connect")}
      </Button>
    </div>
    <div className="github-connection-actions">
      <Button type="link" disabled={github.active || github.retryCreate}
        onClick={event => { trigger.current = event.currentTarget; void github.start("", true); }}>{label("web")}</Button>
      <Button type="link" onClick={() => vm.openCloudSetupModal("github", "auth")}>{label("manual")}</Button>
    </div>
    {github.notice && !github.active && <Alert showIcon type={github.notice === "cancelled" ? "info" : "warning"}
      message={label(github.notice)} action={github.retryCreate
        ? <Button size="small" aria-label={label("retry")} onClick={() => void github.retry()}>{label("retry")}</Button>
        : github.notice === "refreshFailed" ? <Button size="small" onClick={() => void github.refresh()}>{label("refresh")}</Button> : undefined} />}
    <div className="github-connection-accounts">
      {github.accounts.map(account => {
        const name = accountName(account.display_name || "", account.connection_id);
        const active = account.status === "ACTIVE";
        const updating = github.updating.includes(account.connection_id);
        return <div className="github-connection-account" key={account.connection_id} role="group" aria-label={name}>
          <div className="github-connection-identity">
            <strong>{name}</strong>
            <Space size={6} wrap>
              <Tag>{account.token_source === "github_cli" ? "GitHub CLI" : label("manualSource")}</Tag>
              <span>{label(active ? "connected" : "reconnectRequired")}</span>
            </Space>
          </div>
          <Space size="middle" wrap>
            <label className="github-connection-chat">
              <span>{label("chat")}</span>
              <Switch size="small" aria-label={label("chat")} checked={account.provider_options?.chat_enabled === true}
                disabled={!active || updating} loading={updating}
                onChange={enabled => void github.changeAccount(account, enabled)} />
            </label>
            <Button size="small" disabled={updating || github.active || github.retryCreate}
              onClick={event => { trigger.current = event.currentTarget; void vm.handleReauthorizeGitHub(account); }}>{label("reconnect")}</Button>
            <Button size="small" danger disabled={updating} onClick={() => {
              Modal.confirm({ title: label("disconnectTitle"), content: label("disconnectHint"),
                okText: label("disconnect"), cancelText: t("common.cancel"), okButtonProps: { danger: true },
                onOk: () => github.changeAccount(account), });
            }}>{label("disconnect")}</Button>
          </Space>
        </div>;
      })}
    </div>
    <Modal open={github.active} title={label("authorizationTitle")} destroyOnHidden
      afterOpenChange={open => {
        if (open) return;
        const target = trigger.current;
        if (target?.isConnected && !target.hasAttribute("disabled")) target.focus();
        else connectButton.current?.focus();
      }}
      onCancel={() => void github.cancel()} maskClosable={false}
      closable={Boolean(github.session?.session_id)} keyboard={Boolean(github.session?.session_id)}
      footer={<Space>
        {waiting && <Button onClick={() => void github.openBrowser()}>{label(github.browserBlocked ? "retryOpen" : "openBrowser")}</Button>}
        <Button disabled={!github.session?.session_id} onClick={() => void github.cancel()}>{label("cancel")}</Button>
      </Space>}>
      <div aria-live="polite">
        {github.active && github.notice && <Alert type="warning" showIcon message={label(github.notice)} />}
        {waiting ? <>
          <p>{label("deviceHint")}</p>
          <Typography.Text className="github-device-code">{github.session?.user_code}</Typography.Text>
          <p>{label("waiting")}</p>
          {github.browserBlocked && <Alert type="warning" showIcon message={label("browserBlocked")} />}
        </> : <p>{label(github.session?.status === "VERIFYING" ? "verifying" : "probing")}</p>}
      </div>
    </Modal>
  </section>;
}
