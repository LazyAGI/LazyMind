import { Alert, Button, Empty, Modal, Skeleton, Space, Switch, Tabs, Tag, Typography } from "antd";
import { ArrowRightOutlined, PlusOutlined } from "@ant-design/icons";
import { useEffect, useRef, type ComponentRef, type ReactNode } from "react";
import type { CloudDocumentProvidersVm } from "../hooks/useCloudDocumentProviders";
import CloudDocumentCredentialForm from "./CloudDocumentCredentialForm";
import "./githubConnectionSection.scss";

export default function GitHubConnectionSection({ vm, logo, management = false }: {
  vm: CloudDocumentProvidersVm; logo: ReactNode; management?: boolean;
}) {
  const { t, github } = vm;
  const trigger = useRef<HTMLElement | null>(null);
  const connectButton = useRef<ComponentRef<typeof Button>>(null);
  const label = (key: string) => t(`modelProvider.cloudDocuments.githubCli.${key}`);
  const waiting = github.session?.status === "WAITING_USER";
  const accountName = (name?: string | null, id?: string | null) => name || id || label("unnamedAccount");
  const activeAccounts = github.accounts.filter(account => account.status === "ACTIVE");
  const activeNames = [...new Set(activeAccounts.map(account => accountName(account.display_name, account.provider_account_id)))];
  const dialogOpen = Boolean(vm.githubDialogOpen || github.active);
  const manual = vm.githubDialogMode === "manual" && !github.active;
  const locked = github.active || github.retryCreate || vm.feishuSetupSubmitting;

  useEffect(() => {
    if (github.session && ["COMPLETED", "CANCELLED", "EXPIRED", "FAILED"].includes(github.session.status)) {
      vm.setGitHubDialogOpen(false);
    }
  }, [github.session, vm.setGitHubDialogOpen]);

  function closeDialog() {
    if (github.active || github.retryCreate) { void github.cancel(); return; }
    if (vm.feishuSetupSubmitting) return;
    vm.setGitHubDialogOpen(false);
    vm.setFeishuSetupModalOpen(false);
    vm.setFeishuSetupIntent(null);
  }

  return <section className={`github-connection-section${management ? " github-account-management" : " github-connection-summary"}`}
    aria-labelledby="github-connections-title">
    <div className={management ? "github-connection-header" : "model-provider-cloud-doc-resource-row"}>
      {logo}
      <div className="model-provider-cloud-doc-resource-copy">
        <h3 id="github-connections-title">{management ? label("accountManagementTitle") : "GitHub"}</h3>
        <p>{management ? label("accountManagementHint") : activeNames.length
          ? t("modelProvider.cloudDocuments.githubConnected", { account: activeNames.join("、") })
          : github.accounts.length ? label("reconnectRequired") : label("description")}</p>
      </div>
      {!management && <Tag className="model-provider-cloud-doc-resource-status" color={activeAccounts.length ? "success" : "default"}>
        {activeAccounts.length ? t("modelProvider.cloudDocuments.authValid") : github.accounts.length
          ? label("reconnectRequired") : label("notConnected")}
      </Tag>}
      <div className={management ? undefined : "model-provider-cloud-doc-resource-controls"}>
      <Button ref={connectButton} type={management ? "primary" : "link"} disabled={github.active || vm.loading}
        className={management ? undefined : "model-provider-cloud-doc-resource-action"}
        icon={management ? <PlusOutlined aria-hidden="true" /> : undefined}
        onClick={event => {
          if (!management && github.accounts.length) { vm.handleManageGitHubAuth(); return; }
          trigger.current = event.currentTarget; vm.handleOpenGitHubSetup();
        }}>
        {management ? label("addAccount") : github.accounts.length
          ? t("modelProvider.cloudDocuments.manageAccount") : label("connect")}
        {!management && <ArrowRightOutlined aria-hidden="true" />}
      </Button>
      </div>
    </div>
    {github.notice && !dialogOpen && <Alert showIcon type={github.notice === "cancelled" ? "info" : "warning"}
      message={label(github.notice)} action={github.retryCreate
        ? <Space>
          <Button size="small" aria-label={label("retry")} onClick={() => vm.handleOpenGitHubSetup()}>{label("retry")}</Button>
          <Button size="small" onClick={() => void github.cancel()}>{label("cancel")}</Button>
        </Space>
        : github.notice === "refreshFailed" ? <Button size="small" onClick={() => void github.refresh()}>{label("refresh")}</Button> : undefined} />}
    {management && <Skeleton active loading={vm.loading}>
    <div className="github-connection-accounts">
      {!github.accounts.length && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={label("emptyAccounts")} />}
      {github.accounts.map(account => {
        const name = accountName(account.display_name, account.provider_account_id);
        const active = account.status === "ACTIVE";
        const updating = github.updating.includes(account.connection_id);
        return <div className="github-connection-account" key={account.connection_id} role="group" aria-label={name}>
          <div className="github-connection-identity">
            <strong>{name}</strong>
            <Typography.Text type="secondary" className="github-connection-id" title={account.connection_id}>
              {account.connection_id}
            </Typography.Text>
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
    </Skeleton>}
    <Modal open={dialogOpen} title={label("authorizationTitle")} destroyOnHidden
      afterOpenChange={open => {
        if (open) return;
        const target = trigger.current;
        if (target?.isConnected && !target.hasAttribute("disabled")) target.focus();
        else connectButton.current?.focus();
      }}
      onCancel={closeDialog} maskClosable={false}
      closable={!vm.feishuSetupSubmitting && (!github.active || Boolean(github.session?.session_id))}
      keyboard={!vm.feishuSetupSubmitting && (!github.active || Boolean(github.session?.session_id))}
      footer={<Space>
        {waiting && <Button onClick={() => void github.openBrowser()}>{label(github.browserBlocked ? "retryOpen" : "openBrowser")}</Button>}
        <Button disabled={vm.feishuSetupSubmitting} onClick={closeDialog}>
          {github.active || github.retryCreate ? label("cancel") : t("common.cancel")}
        </Button>
        {!github.active && (manual
          ? <Button type="primary" loading={vm.feishuSetupSubmitting} onClick={() => void vm.handleSaveFeishuSetup()}>
            {t("modelProvider.cloudDocuments.credentialSaveAndAuthorize")}
          </Button>
          : <Button type="primary" aria-label={label(github.retryCreate ? "retry" : "start")}
            onClick={() => void (github.retryCreate ? github.retry() : github.start("", management))}>
            {label(github.retryCreate ? "retry" : "start")}
          </Button>)}
      </Space>}>
      <Tabs activeKey={manual ? "manual" : "quick"} onChange={mode => {
        if (locked) return;
        if (mode === "manual") vm.openCloudSetupModal("github", "auth");
        else {
          vm.setGitHubDialogMode("quick");
          vm.setFeishuSetupModalOpen(false);
          vm.setFeishuSetupIntent(null);
        }
      }} items={[
        { key: "quick", label: label("quickMode"), disabled: locked && manual, children: <div aria-live="polite">
        {github.notice && <Alert type="warning" showIcon message={label(github.notice)} />}
        {waiting ? <>
          <p>{label("deviceHint")}</p>
          <Typography.Text className="github-device-code">{github.session?.user_code}</Typography.Text>
          <p>{label("waiting")}</p>
          {github.browserBlocked && <Alert type="warning" showIcon message={label("browserBlocked")} />}
        </> : github.active ? <p>{label(github.session?.status === "VERIFYING" ? "verifying" : "probing")}</p> : <>
          <p>{label("quickHint")}</p>
          <Typography.Paragraph type="secondary">{label("quickAvailability")}</Typography.Paragraph>
          <Button type="link" disabled={github.retryCreate} onClick={() => void github.start("", true)}>{label("web")}</Button>
        </>}
        </div> },
        { key: "manual", label: label("manual"), disabled: locked && !manual,
          children: <CloudDocumentCredentialForm vm={vm} /> },
      ]} />
    </Modal>
  </section>;
}
