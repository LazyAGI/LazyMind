import { Button, Spin, message, Modal, Select, Flex, Input } from "antd";
import type { SelectProps } from "antd";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import moment from "moment";
import { DatasetAclEnum } from "@/api/generated/knowledge-client";
import type {
  Dataset,
  DatasetMember as CoreDatasetMember,
  DatasetRole as CoreDatasetRole,
} from "@/api/generated/core-client";
import { useNavigate } from "react-router-dom";

import {
  MemberType,
  ROLE_TITLE_MAP,
  ROLE_TYPE_INFO,
} from "@/modules/knowledge/constants/common";
import AddUserModal, { IAddUserModalRef } from "../AddUserModal";
import {
  KnowledgeBaseServiceApi,
  MemberServiceApi,
} from "@/modules/knowledge/utils/request";
import { ListPageTable } from "@/components/ui";
const { confirm } = Modal;
const { Search } = Input;

interface IProps {
  memberType: MemberType;
  detail: Dataset | undefined;
}

type MemberRecord = CoreDatasetMember & Member;

interface Member {
  id: string;
  type: MemberType;
  display_name: string;
  is_creator?: boolean;
}

const MemberList = (props: IProps) => {
  const { t } = useTranslation();
  const [searchValue, setSearchValue] = useState("");
  const [dataSource, setDataSource] = useState<MemberRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [pagination, setPagination] = useState({
    current: 1,
    pageSize: 10,
    showSizeChanger: false,
  });
  const [currentDetail, setCurrentDetail] = useState<Dataset>();
  const [updatingMemberId, setUpdatingMemberId] = useState<string>();

  const addUserModalRef = useRef<IAddUserModalRef>();

  const navigate = useNavigate();

  const { memberType, detail } = props;

  const isGroup = memberType === MemberType.GROUP;
  const roleOptions: SelectProps["options"] = ROLE_TYPE_INFO.map((item) => ({
    value: item.id,
    label: item.title,
    // disabled: item.id === RoleType.OWNER,
  }));

  const showDataSource = dataSource.filter((item: MemberRecord) => {
    return (
      !searchValue ||
      item?.display_name?.toLowerCase().includes(searchValue?.toLowerCase())
    );
  });

  const columns = [
    {
      title: isGroup ? t("knowledge.groupName") : t("knowledge.userName"),
      dataIndex: "display_name",
    },
    {
      title: t("knowledge.role"),
      dataIndex: "role",
      width: 156,
      render: (role: CoreDatasetRole, record: MemberRecord) => {
        const canEditRole =
          currentDetail?.acl?.includes(DatasetAclEnum.DatasetWrite) &&
          !isCreator(record);

        return (
          <Select
            value={role?.role}
            disabled={!canEditRole}
            loading={updatingMemberId === record.id}
            options={
              canEditRole
                ? roleOptions
                : [
                    {
                      value: role?.role,
                      label: ROLE_TITLE_MAP[role?.role || ""] || role?.display_name || role?.role,
                    },
                  ]
            }
            style={{ width: "100%" }}
            onChange={(value) => handleUpdateRole(record, value as string)}
          />
        );
      },
    },
    // TODO remove date this version.
    // {
    //   dataIndex: 'create_time',
    //   width: 200,
    //   render: (text: string) => (moment(text).isValid() ? moment(text).format('YYYY-MM-DD HH:mm:ss') : ''),
    // },
    {
      title: t("common.actions"),
      key: "action",
      width: 102,
      render: (record: MemberRecord) => {
        const canDeleteMemberPermission =
          currentDetail?.acl?.includes(DatasetAclEnum.DatasetWrite) &&
          !isCreator(record);

        return (
          canDeleteMemberPermission && (
            <Button
              type="link"
              danger
              onClick={() => handleDelete(record)}
              style={{ padding: 0, minWidth: "auto" }}
            >
              {t("common.delete")}
            </Button>
          )
        );
      },
    },
  ];

  useEffect(() => {
    setCurrentDetail(detail);
    if (!detail?.acl?.includes(DatasetAclEnum.DatasetWrite)) {
      // When a non-creator deletes or downgrades their own permissions, they will no longer be able to access the knowledge base and need to return to the knowledge base list page.
      navigate({
        pathname: "/lib/knowledge/list",
      });
      return;
    }

    getTableData(detail);
  }, []);

  function onSearch(value: string) {
    const str = value?.trim() || "";
    setSearchValue(str);
    pagination.current = 1;
    setPagination({ ...pagination });
  }

  function getTableData(knowledgeBaseDetail: Dataset) {
    setLoading(true);
    // The interface cannot support distinguishing users and user groups and can only be implemented on the front end.
    MemberServiceApi()
      .datasetMemberServiceListDatasetMembers({
        dataset: knowledgeBaseDetail?.dataset_id || "",
      })
      .then((res) => {
        const list = (res.data.dataset_members || [])
          .map((item: CoreDatasetMember) => {
            const groupUser = !!item.group_id;
            return {
              ...item,
              type: groupUser ? MemberType.GROUP : MemberType.USER,
              id: (groupUser ? item.group_id : item.user_id) || "",
              display_name: (groupUser ? item.group : item.user) || "",
            };
          })
          .filter((item) => item.type === memberType && item.id)
          .sort((a, b) => {
            return (
              moment(b?.create_time).valueOf() -
              moment(a?.create_time).valueOf()
            );
          });
        setDataSource(list);
        pagination.current = 1;
        setPagination({ ...pagination });
      })
      .finally(() => {
        setLoading(false);
      });
  }

  function isCreator(record: MemberRecord) {
    return !!record.is_creator;
  }

  function handleUpdateRole(record: MemberRecord, nextRole: string) {
    if (isCreator(record)) {
      message.error(t("knowledge.editOwnerPermissionDenied"));
      return;
    }

    if (!record.role || record.role.role === nextRole) {
      return;
    }

    setUpdatingMemberId(record.id);
    MemberServiceApi()
      .datasetMemberServiceUpdateDatasetMember({
        dataset: record.dataset_id || "",
        ...(record.type === MemberType.GROUP
          ? { groupId: record.id }
          : { userId: record.id }),
        datasetMember: {
          ...(record.type === MemberType.GROUP
            ? { group_id: record.id }
            : { user_id: record.id }),
          role: {
            role: nextRole,
            display_name: ROLE_TITLE_MAP[nextRole] || nextRole,
          },
        },
        updateMask: "role",
      })
      .then(() => {
        message.success(t("common.saveSuccess"));
        fetchDetail();
      })
      .catch((err) => {
        console.error("Update knowledge base member role error: ", err);
      })
      .finally(() => {
        setUpdatingMemberId(undefined);
      });
  }

  function handleDelete(record: MemberRecord) {
    if (isCreator(record)) {
      message.error(t("knowledge.deleteOwnerPermissionDenied"));
      return;
    }

    confirm({
      title: t("knowledge.deletePermissionTitle"),
      content: t("knowledge.deletePermissionContent", {
        type: isGroup ? t("knowledge.groups") : t("knowledge.users"),
        name: record.display_name,
        role:
          ROLE_TITLE_MAP[record.role?.role || ""] ||
          record.role?.display_name ||
          "-",
      }),
      centered: true,
      okType: "danger",
      onOk() {
        return new Promise((resolve, reject) => {
          MemberServiceApi()
            .datasetMemberServiceDeleteDatasetMember({
              dataset: record.dataset_id || "",
              ...(record.type === MemberType.GROUP
                ? { groupId: record.id }
                : { userId: record.id }),
            })
            .then(() => {
              resolve("");
              message.success(
                isGroup
                  ? t("knowledge.deleteGroupSuccess")
                  : t("knowledge.deleteUserSuccess"),
              );
              fetchDetail();
            })
            .catch((err) => {
              console.error("Delete knowledge base user/group error: ", err);
              reject(false);
            });
        });
      },
    });
  }

  function handleTableChange(paginationInfo: any) {
    setPagination(paginationInfo);
  }

  function fetchDetail() {
    KnowledgeBaseServiceApi()
      .datasetServiceGetDataset({ dataset: currentDetail?.dataset_id || "" })
      .then((res) => {
        const nextDetail = res.data;
        setCurrentDetail(nextDetail);
        getTableData(nextDetail);
      })
      .catch((err) => {
        if (err?.response?.data?.code === 10104) {
          // When a non-creator deletes or downgrades their own permissions, they will no longer be able to access the knowledge base and need to return to the knowledge base list page.
          navigate({
            pathname: "/lib/knowledge/list",
          });
          return;
        }
      });
  }

  return (
    <Spin spinning={loading}>
      <Flex
        style={{
          width: "100%",
          justifyContent: "space-between",
          marginBottom: "16px",
        }}
      >
        <Search
          placeholder={isGroup ? t("knowledge.groupName") : t("knowledge.userName")}
          onSearch={onSearch}
          style={{ width: 300 }}
          allowClear
        />
        {currentDetail?.acl?.includes(DatasetAclEnum.DatasetWrite) && (
          <Button
            type="primary"
            onClick={() =>
              addUserModalRef.current?.handleOpen({
                dataset_id: currentDetail.dataset_id || "",
                memberType,
              })
            }
          >
            {isGroup ? t("knowledge.addGroup") : t("knowledge.addUser")}
          </Button>
        )}
      </Flex>
      <ListPageTable
        columns={columns as any}
        dataSource={showDataSource}
        rowKey="user_id"
        pagination={pagination}
        onChange={handleTableChange}
        className="w-full"
        scroll={{
          y: "calc(100vh - 350px)",
        }}
      />

      <AddUserModal
        ref={addUserModalRef}
        onOk={() => {
          fetchDetail();
        }}
      />
    </Spin>
  );
};

export default MemberList;
