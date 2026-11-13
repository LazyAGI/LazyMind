import { useEffect, useRef, useState } from "react";
import { Alert, Button, Modal, Space, Tag, Typography } from "antd";
import { CodeOutlined, RightOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useLocation } from "react-router-dom";
import { getNodeDependencyStatus, installNodeDependency, type NodeDependencyStatus } from "../api/systemDependencies";

export default function NodeDependencyCard({ visible }: { visible: boolean }) {
  const { t } = useTranslation();
  const location = useLocation();
  const [open, setOpen] = useState(false);
  const [status, setStatus] = useState<NodeDependencyStatus>();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState(false);
  const mounted = useRef(false);
  const busy = useRef(false);
  const load = async (install = false) => {
    if (busy.current) return;
    busy.current = true;
    setPending(true);
    setError(false);
    try {
      const result = await (install ? installNodeDependency() : getNodeDependencyStatus());
      if (mounted.current) setStatus(result);
    } catch {
      if (mounted.current) setError(true);
    } finally {
      busy.current = false;
      if (mounted.current) setPending(false);
    }
  };
  useEffect(() => {
    mounted.current = true;
    void load();
    return () => { mounted.current = false; };
  }, []);
  useEffect(() => {
    if (location.hash === "#node-dependency") setOpen(true);
  }, [location.hash]);
  return <>
    {visible && <button type="button" id="node-dependency" className="model-provider-service-card tone-violet" onClick={() => setOpen(true)}>
      <span className="model-provider-service-logo"><CodeOutlined /></span>
      <div className="model-provider-service-card-copy">
        <div className="model-provider-service-title-row"><h4>{t("nodeDependency.title")}</h4><Tag color={status?.installed ? "success" : "default"}>{t(`nodeDependency.${pending ? "checking" : status?.installed ? "ready" : "missing"}`)}</Tag></div>
        <p>{t("nodeDependency.summary")}</p>
      </div><RightOutlined />
    </button>}
    <Modal open={open} title={t("nodeDependency.title")} onCancel={() => setOpen(false)} closable={!pending} maskClosable={!pending} keyboard={!pending}
      footer={<Space><Button disabled={pending} onClick={() => void load()}>{t("nodeDependency.refresh")}</Button>{!status?.installed && <Button type="primary" loading={pending} disabled={!status?.installSupported} onClick={() => void load(true)}>{t("nodeDependency.install")}</Button>}</Space>}>
      <Typography.Paragraph>{t("nodeDependency.description")}</Typography.Paragraph>
      {error && <Alert type="error" showIcon message={t("nodeDependency.failed")} />}
      {status?.installed && <><Tag color="success">{status.version}</Tag><Typography.Paragraph copyable>{status.path}</Typography.Paragraph></>}
      {status && !status.installed && !status.installSupported && <Alert type="warning" showIcon message={t("nodeDependency.unsupported")} />}
    </Modal>
  </>;
}
