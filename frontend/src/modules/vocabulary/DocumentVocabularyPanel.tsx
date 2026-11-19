import { Alert, Button, Dropdown, Empty, Modal, Spin, Tag, theme } from "antd";
import { DownOutlined, MoreOutlined } from "@ant-design/icons";
import { useEffect, useState, type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import {
  deleteDocumentVocabulary,
  listDocumentVocabulary,
  removeDocumentVocabulary,
  type DocumentVocabularyItem,
} from "./api";
import "./DocumentVocabularyPanel.scss";

type PanelProps = { documentId: string; refreshToken?: number };

// A document change also resets pending confirmations' refresh callbacks and data.
export default function DocumentVocabularyPanel(props: PanelProps) {
  return <DocumentVocabularyContent key={props.documentId} {...props} />;
}

function DocumentVocabularyContent({ documentId, refreshToken = 0 }: PanelProps) {
  const { t } = useTranslation();
  const { token } = theme.useToken();
  const [items, setItems] = useState<DocumentVocabularyItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const [reload, setReload] = useState(0);
  const refresh = () => setReload((value) => value + 1);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(false);
    listDocumentVocabulary(documentId)
      .then((result) => { if (active) setItems(result); })
      .catch(() => { if (active) setError(true); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [documentId, refreshToken, reload]);

  const confirmAction = (item: DocumentVocabularyItem, deleteWord: boolean) => {
    Modal.confirm({
      title: t(`vocabulary.document.${deleteWord ? "deleteTitle" : "removeTitle"}`, { term: item.term }),
      content: t(`vocabulary.document.${deleteWord ? "deleteDescription" : "removeDescription"}`),
      okButtonProps: { danger: deleteWord },
      onOk: async () => {
        await (deleteWord ? deleteDocumentVocabulary : removeDocumentVocabulary)(documentId, item.id);
        refresh();
      },
    });
  };

  const style = {
    "--vocabulary-card-bg": token.colorBgContainer,
    "--vocabulary-card-border": token.colorBorder,
    "--vocabulary-text": token.colorText,
    "--vocabulary-secondary": token.colorTextSecondary,
    "--vocabulary-source-bg": token.colorFillQuaternary,
    "--vocabulary-danger": token.colorError,
    "--vocabulary-danger-hover": token.colorErrorHover,
    "--vocabulary-danger-bg": token.colorErrorBg,
  } as CSSProperties;

  return (
    <section className="document-vocabulary-panel" style={style}>
      {loading ? (
        <div className="document-vocabulary-panel__state" role="status" aria-label={t("vocabulary.document.loading", { defaultValue: "Loading vocabulary…" })}><Spin /></div>
      ) : error ? (
        <Alert type="error" showIcon message={t("vocabulary.document.loadFailed", { defaultValue: "Unable to load document vocabulary." })}
          action={<Button size="small" onClick={refresh}>{t("vocabulary.document.retry", { defaultValue: "Retry" })}</Button>} />
      ) : !items.length ? (
        <Empty className="document-vocabulary-panel__state" description={t("vocabulary.document.empty")} />
      ) : (
        <div className="document-vocabulary-panel__masonry">
          {items.map((item) => {
            const sentence = item.source.context_sentence || item.example?.sentence;
            // An example translation belongs only to its matching source sentence.
            const translation = !item.source.context_sentence || item.source.context_sentence === item.example?.sentence
              ? item.example?.translation : undefined;
            return (
              <article className="document-vocabulary-card" key={item.id} aria-label={item.term}>
                <header className="document-vocabulary-card__header">
                  <div className="document-vocabulary-card__identity"><h3>{item.term}</h3>
                  {item.phonetic && <span className="document-vocabulary-card__phonetic">{item.phonetic}</span>}
                  </div>
                  <Dropdown destroyOnHidden trigger={["click"]} menu={{ items: [
                    { key: "remove", label: t("vocabulary.document.removeSource") },
                    ...(item.can_delete ? [{ key: "delete", label: t("vocabulary.document.deleteWord"), danger: true }] : []),
                  ], onClick: ({ key }) => confirmAction(item, key === "delete") }}>
                    <Button className="document-vocabulary-card__more" type="text" size="small" icon={<MoreOutlined />} aria-label={t("vocabulary.document.moreActions", { term: item.term })} />
                  </Dropdown>
                </header>
                <div className="document-vocabulary-card__meaning">
                  <span>{item.meaning}</span>
                </div>
                <details className="document-vocabulary-card__details">
                  <summary aria-label={t("vocabulary.document.exampleDetails")} title={t("vocabulary.document.exampleDetails")}><DownOutlined aria-hidden="true" /></summary>
                  {item.definition && <p>{item.definition}</p>}
                {sentence && (
                  <blockquote className="document-vocabulary-card__source">
                    <p>{sentence}</p>
                    {translation && <p className="document-vocabulary-card__translation">{translation}</p>}
                  </blockquote>
                )}
                <div className="document-vocabulary-card__location">
                  <span>{t("vocabulary.document.sourceLocation", { location: item.source.page != null
                    ? t("vocabulary.document.page", { page: item.source.page })
                    : item.source.segment_id || t("vocabulary.document.document") })}</span>
                  {item.source_count > 1 && <Tag>{t("vocabulary.document.sourceCount", { count: item.source_count })}</Tag>}
                </div>
                </details>
              </article>
            );
          })}
        </div>
      )}
    </section>
  );
}
