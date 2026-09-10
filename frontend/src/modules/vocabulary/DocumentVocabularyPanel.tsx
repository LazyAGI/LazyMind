import { Button, Empty, List, Modal, Space, Spin, Tag } from "antd";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  deleteDocumentVocabulary,
  listDocumentVocabulary,
  removeDocumentVocabulary,
  type DocumentVocabularyItem,
} from "./api";

export default function DocumentVocabularyPanel({
  documentId,
  refreshToken = 0,
}: {
  documentId: string;
  refreshToken?: number;
}) {
  const { t } = useTranslation();
  const [items, setItems] = useState<DocumentVocabularyItem[]>([]);
  const [loading, setLoading] = useState(true);
  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await listDocumentVocabulary(documentId));
    } finally {
      setLoading(false);
    }
  }, [documentId]);
  useEffect(() => {
    void refresh();
  }, [refresh, refreshToken]);
  if (loading) return <Spin />;
  if (!items.length)
    return <Empty description={t("vocabulary.document.empty")} />;
  return (
    <List
      dataSource={items}
      renderItem={(item) => {
        const actions = [
          <Button
            key="remove"
            size="small"
            onClick={() =>
              Modal.confirm({
                title: t("vocabulary.document.removeTitle", {
                  term: item.term,
                }),
                content: t("vocabulary.document.removeDescription"),
                onOk: () =>
                  removeDocumentVocabulary(documentId, item.id).then(refresh),
              })
            }
          >
            {t("vocabulary.document.removeSource")}
          </Button>,
        ];
        if (item.can_delete)
          actions.push(
            <Button
              key="delete"
              size="small"
              danger
              onClick={() =>
                Modal.confirm({
                  title: t("vocabulary.document.deleteTitle", {
                    term: item.term,
                  }),
                  content: t("vocabulary.document.deleteDescription"),
                  okButtonProps: { danger: true },
                  onOk: () =>
                    deleteDocumentVocabulary(documentId, item.id).then(refresh),
                })
              }
            >
              {t("vocabulary.document.deleteWord")}
            </Button>,
          );
        return (
          <List.Item actions={actions}>
            <List.Item.Meta
              title={
                <span>
                  {item.term} <Tag>{item.provider}</Tag>
                  {item.source_count > 1 ? (
                    <Tag color="blue">
                      {t("vocabulary.document.sourceCount", {
                        count: item.source_count,
                      })}
                    </Tag>
                  ) : null}
                </span>
              }
              description={
                <Space direction="vertical" size={2}>
                  <span>
                    {item.part_of_speech} {item.meaning}
                  </span>
                  {item.example ? (
                    <span>
                      {item.example.sentence}
                      <br />
                      {item.example.translation}
                    </span>
                  ) : null}
                  <span>
                    {t("vocabulary.document.sourceLocation", {
                      location: item.source.page
                        ? t("vocabulary.document.page", {
                            page: item.source.page,
                          })
                        : item.source.segment_id ||
                          t("vocabulary.document.document"),
                    })}
                  </span>
                </Space>
              }
            />
          </List.Item>
        );
      }}
    />
  );
}
