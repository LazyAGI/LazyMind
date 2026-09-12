import { Tabs } from "antd";
import { useNavigate, useParams, useLocation } from "react-router-dom";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { KnowledgeBaseServiceApi } from "@/modules/knowledge/utils/request";

import { MemberType } from "@/modules/knowledge/constants/common";
import MemberList from "./components/MemberList";
import type { Dataset } from "@/api/generated/core-client";
import { DetailPageHeader } from "@/components/ui";

const Authorize = () => {
  const { t } = useTranslation();
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const location = useLocation();

  const [detail, setDetail] = useState<Dataset>();

  const searchParams = new URLSearchParams(location.search);

  useEffect(() => {
    fetchDetail();
  }, []);

  function fetchDetail() {
    KnowledgeBaseServiceApi()
      .datasetServiceGetDataset({ dataset: id })
      .then((res) => {
        setDetail(res.data);
      });
  }

  return (
    <div className="knowledge-container w-full !items-start">
      <DetailPageHeader
        className="mb-4"
        breadcrumbs={[
          { title: t("layout.knowledgeBase"), href: "/lib/knowledge/list" },
          { title: detail?.display_name },
        ]}
        title={t("knowledge.authorizeTitle", { name: detail?.display_name })}
        onBack={() => {
          navigate(-1);
        }}
      />
      <Tabs
        defaultActiveKey={`${searchParams.get("tab") || MemberType.USER}`}
        onChange={(v) => {
          searchParams.set("tab", v);
          navigate(`${location.pathname}?${searchParams.toString()}`, {
            replace: true,
          });
        }}
        className="w-full"
      >
        <Tabs.TabPane tab={t("knowledge.users")} key={`${MemberType.USER}`}>
          {detail && (
            <MemberList memberType={MemberType.USER} detail={detail} />
          )}
        </Tabs.TabPane>
        <Tabs.TabPane tab={t("knowledge.groups")} key={`${MemberType.GROUP}`}>
          {detail && (
            <MemberList memberType={MemberType.GROUP} detail={detail} />
          )}
        </Tabs.TabPane>
      </Tabs>
    </div>
  );
};

export default Authorize;
