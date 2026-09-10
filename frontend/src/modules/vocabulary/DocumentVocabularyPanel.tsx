import { Button, Empty, List, Modal, Space, Spin, Tag } from "antd";
import { useCallback, useEffect, useState } from "react";
import { deleteDocumentVocabulary, listDocumentVocabulary, removeDocumentVocabulary, type DocumentVocabularyItem } from "./api";

export default function DocumentVocabularyPanel({ documentId, refreshToken = 0 }: { documentId: string; refreshToken?: number }) {
  const [items, setItems] = useState<DocumentVocabularyItem[]>([]); const [loading, setLoading] = useState(true);
  const refresh = useCallback(async () => { setLoading(true); try { setItems(await listDocumentVocabulary(documentId)); } finally { setLoading(false); } }, [documentId]);
  useEffect(() => { void refresh(); }, [refresh, refreshToken]);
  if (loading) return <Spin />;
  if (!items.length) return <Empty description="本文档还没有添加生词" />;
  return <List dataSource={items} renderItem={(item) => {const actions=[<Button key="remove" size="small" onClick={()=>Modal.confirm({title:`从本文档移除 ${item.term}？`,content:"只移除当前文档来源；单词、其他来源和复习记录都会保留。",onOk:()=>removeDocumentVocabulary(documentId,item.id).then(refresh)})}>移除来源</Button>];if(item.can_delete)actions.push(<Button key="delete" size="small" danger onClick={()=>Modal.confirm({title:`彻底删除 ${item.term}？`,content:"这是该单词的最后一个来源。删除后，相关例句和复习数据也会删除。",okButtonProps:{danger:true},onOk:()=>deleteDocumentVocabulary(documentId,item.id).then(refresh)})}>删除单词</Button>);return <List.Item actions={actions}><List.Item.Meta title={<span>{item.term} <Tag>{item.provider}</Tag>{item.source_count>1?<Tag color="blue">{item.source_count} 个来源</Tag>:null}</span>} description={<Space direction="vertical" size={2}><span>{item.part_of_speech} {item.meaning}</span>{item.example ? <span>{item.example.sentence}<br />{item.example.translation}</span> : null}<span>来源位置：{item.source.page?`第 ${item.source.page} 页`:item.source.segment_id||"文档"}</span></Space>} /></List.Item>}} />;
}
