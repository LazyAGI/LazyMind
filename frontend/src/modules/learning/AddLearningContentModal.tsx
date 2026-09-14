import { Alert, Button, Form, Input, Modal, Select, Space, Spin, message } from "antd";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { PdfTextSelection } from "@/components/ui";
import { getLocalizedErrorMessage } from "@/components/request";
import { createLearningBook, getLearningCatalog, listLearningBooks, resolveLearningContent, type LearningBook, type LearningCapability } from "./api";

export interface LearningSelection { capabilityKey:string; selection:PdfTextSelection }
interface Props { value:LearningSelection|null; datasetId:string; documentId:string; segmentId?:string; context?:string; onClose:()=>void; onAdded:()=>void }

export default function AddLearningContentModal({value,datasetId,documentId,segmentId,context,onClose,onAdded}:Props){
  const {t}=useTranslation(); const [form]=Form.useForm(); const [capability,setCapability]=useState<LearningCapability>(); const [books,setBooks]=useState<LearningBook[]>([]); const [loading,setLoading]=useState(false); const [saving,setSaving]=useState(false); const [source,setSource]=useState(""); const [quickName,setQuickName]=useState("");
  const request=useMemo(()=>value?{capability_key:value.capabilityKey,text:value.selection.text,context:value.selection.context||context||value.selection.text,dataset_id:datasetId,document_id:documentId,segment_id:segmentId||"",page:value.selection.page}:null,[context,datasetId,documentId,segmentId,value]);
  useEffect(()=>{ if(!request)return; setLoading(true); Promise.all([getLearningCatalog(),listLearningBooks()]).then(async ([catalog,allBooks])=>{ const def=catalog.capabilities.find(x=>x.key===request.capability_key); if(!def)throw new Error(t("learning.unsupportedCapability")); setCapability(def); const compatible=allBooks.filter(x=>x.capability_key===def.key); setBooks(compatible); const resolved=await resolveLearningContent({...request,preview:true}); setSource(resolved.source); form.setFieldsValue({...resolved.value,book_ids:compatible.slice(0,1).map(x=>x.id)}); }).catch(e=>message.error(getLocalizedErrorMessage(e))).finally(()=>setLoading(false)); },[form,request,t]);
  const fields=useMemo(()=>capability?.fields||[],[capability]);
  const save=async()=>{ try{const values=await form.validateFields(); const {book_ids,...schemaValue}=values; if(!request)return; setSaving(true); await resolveLearningContent({...request,value:schemaValue,book_ids:book_ids||[]}); message.success(t("learning.added")); onAdded(); onClose();}catch(e){message.error(getLocalizedErrorMessage(e))}finally{setSaving(false)}};
  const createCompatibleBook=async()=>{if(!capability||!quickName.trim())return;const book=await createLearningBook({name:quickName.trim(),capability_key:capability.key,question_types:capability.default_question_types});setBooks([book]);form.setFieldValue("book_ids",[book.id]);setQuickName("")};
  return <Modal open={!!value} title={capability?t(capability.name_i18n_key):t("learning.resolveContent")} onCancel={onClose} footer={[<Button key="cancel" onClick={onClose}>{t("common.cancel")}</Button>,<Button key="save" type="primary" loading={saving} disabled={loading} onClick={save}>{t("learning.addToCollection")}</Button>]} destroyOnHidden>
    <Spin spinning={loading}><Form form={form} layout="vertical">
      {source?<Alert type="info" showIcon message={t("learning.contentSource",{source})} style={{marginBottom:16}}/>:null}
      {fields.map(field=><Form.Item key={field.key} name={field.key} label={t(field.label_i18n_key)} extra={field.help_i18n_key?t(field.help_i18n_key):undefined} rules={field.required?[{required:true,message:t("learning.requiredField")}]:undefined}>
        {field.type==="text"?<Input.TextArea rows={3} disabled={!field.editable}/>:field.type==="string_list"?<Select mode="tags" disabled={!field.editable}/>:<Input disabled={!field.editable}/>}</Form.Item>)}
      <Form.Item name="book_ids" label={t("learning.learningCollections")} rules={[{required:true,message:t("learning.selectCollection")}]}><Select mode="multiple" options={books.map(book=>({value:book.id,label:book.name}))} placeholder={books.length?t("learning.selectCollection"):t("learning.noCompatibleCollection")}/></Form.Item>
      {!books.length?<Space.Compact style={{width:"100%"}}><Input value={quickName} onChange={event=>setQuickName(event.target.value)} placeholder={t("learning.newCompatibleCollectionName")}/><Button onClick={()=>void createCompatibleBook()} disabled={!quickName.trim()}>{t("learning.createCompatibleCollection")}</Button></Space.Compact>:null}
    </Form></Spin>
  </Modal>
}
