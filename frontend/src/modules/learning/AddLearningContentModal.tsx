import { Button, Form, Input, Modal, Select, Space, Spin, Tag, Typography, message } from "antd";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { PdfTextSelection } from "@/components/ui";
import { getLocalizedErrorMessage } from "@/components/request";
import { createLearningBook, getLearningCatalog, listLearningBooks, resolveLearningContent, type LearningBook, type LearningCapability } from "./api";
import { capabilityFamily, capabilityFamilyI18nKey } from "./capabilityFamilies";
import "./AddLearningContentModal.scss";

export interface LearningSelection { capabilityKey:string; selection:PdfTextSelection }
interface Props { value:LearningSelection|null; datasetId:string; documentId:string; segmentId?:string; context?:string; onClose:()=>void; onAdded:()=>void }

export default function AddLearningContentModal({value,datasetId,documentId,segmentId,context,onClose,onAdded}:Props){
  const {t}=useTranslation(); const [form]=Form.useForm(); const [capability,setCapability]=useState<LearningCapability>(); const [books,setBooks]=useState<LearningBook[]>([]); const [loading,setLoading]=useState(false); const [saving,setSaving]=useState(false); const [editing,setEditing]=useState(false); const [source,setSource]=useState(""); const [resolvedValue,setResolvedValue]=useState<Record<string,unknown>>({}); const [dictionaryMeaning,setDictionaryMeaning]=useState(""); const [quickName,setQuickName]=useState("");
  const request=useMemo(()=>value?{capability_key:value.capabilityKey,text:value.selection.text,context:value.selection.context||context||value.selection.text,dataset_id:datasetId,document_id:documentId,segment_id:segmentId||"",page:value.selection.page}:null,[context,datasetId,documentId,segmentId,value]);
  useEffect(()=>{ if(!request)return; let active=true; setCapability(undefined);setBooks([]);setSource("");setResolvedValue({});setEditing(false);setLoading(true);setDictionaryMeaning(""); getLearningCatalog().then(async catalog=>{ const def=catalog.capabilities.find(x=>x.key===request.capability_key); if(!def)throw new Error(t("learning.unsupportedCapability")); const [resolved,allBooks]=await Promise.all([resolveLearningContent({...request,preview:true}),capabilityFamily(def.key)==="translation"?listLearningBooks():Promise.resolve([])]); if(!active)return; setCapability(def);setBooks(allBooks.filter(x=>x.capability_key===def.key));setSource(resolved.source);setResolvedValue(resolved.value);setDictionaryMeaning(String(resolved.value.dictionary_meaning||"")); }).catch(e=>{if(active)message.error(getLocalizedErrorMessage(e))}).finally(()=>{if(active)setLoading(false)}); return()=>{active=false}; },[request,t]);
  useEffect(()=>{if(!editing)return;form.resetFields();form.setFieldsValue({...resolvedValue,book_ids:books.slice(0,1).map(book=>book.id)})},[editing]);
  const fields=useMemo(()=>capability?.fields||[],[capability]);
  const canAddToCollection=capabilityFamily(capability?.key||"")==="translation";
  const save=async()=>{ try{const values=await form.validateFields(); const {book_ids,...schemaValue}=values; if(!request)return; setSaving(true); await resolveLearningContent({...request,value:dictionaryMeaning?{...schemaValue,dictionary_meaning:dictionaryMeaning}:schemaValue,book_ids:book_ids||[]}); message.success(t("learning.added")); onAdded(); onClose();}catch(e){message.error(getLocalizedErrorMessage(e))}finally{setSaving(false)}};
  const createCompatibleBook=async()=>{if(!capability||!quickName.trim())return;const book=await createLearningBook({name:quickName.trim(),capability_key:capability.key,question_types:capability.default_question_types});setBooks([book]);form.setFieldValue("book_ids",[book.id]);setQuickName("")};
  const displayValue=(value:unknown)=>Array.isArray(value)?<ul>{value.map((item,index)=><li key={index}>{typeof item==="object"?JSON.stringify(item):String(item)}</li>)}</ul>:<Typography.Paragraph>{typeof value==="object"?JSON.stringify(value):String(value||"—")}</Typography.Paragraph>;
  const close=()=>{setEditing(false);onClose()};
  return <Modal className="learning-content-modal" open={!!value} title={capability?t(capabilityFamilyI18nKey(capabilityFamily(capability.key))):t("learning.resolveContent")} onCancel={close} footer={editing?[<Button key="back" onClick={()=>setEditing(false)}>{t("common.back")}</Button>,<Button key="save" type="primary" loading={saving} disabled={loading} onClick={save}>{t("learning.confirmAddToCollection")}</Button>]:[<Button key="cancel" onClick={close}>{t("common.close")}</Button>,canAddToCollection?<Button key="edit" type="primary" disabled={loading||!capability} onClick={()=>setEditing(true)}>{t("learning.addToCollection")}</Button>:null]} destroyOnHidden>
    <Spin spinning={loading}>{editing?<Form form={form} layout="vertical">
      {fields.map(field=><Form.Item key={field.key} name={field.key} label={t(field.label_i18n_key)} extra={field.help_i18n_key?t(field.help_i18n_key):undefined} rules={field.required?[{required:true,message:t("learning.requiredField")}]:undefined}>
        {field.type==="text"?<Input.TextArea rows={3} disabled={!field.editable}/>:field.type==="string_list"?<Select mode="tags" disabled={!field.editable}/>:<Input disabled={!field.editable}/>}</Form.Item>)}
      <Form.Item name="book_ids" label={t("learning.learningCollections")} rules={[{required:true,message:t("learning.selectCollection")}]}><Select mode="multiple" options={books.map(book=>({value:book.id,label:book.name}))} placeholder={books.length?t("learning.selectCollection"):t("learning.noCompatibleCollection")}/></Form.Item>
      {!books.length?<Space.Compact style={{width:"100%"}}><Input value={quickName} onChange={event=>setQuickName(event.target.value)} placeholder={t("learning.newCompatibleCollectionName")}/><Button onClick={()=>void createCompatibleBook()} disabled={!quickName.trim()}>{t("learning.createCompatibleCollection")}</Button></Space.Compact>:null}
    </Form>:<div className="learning-content-preview">
      {source?<div className="learning-content-preview__source"><Tag color="blue">{t("learning.contentSource",{source})}</Tag></div>:null}
      {dictionaryMeaning?<section><Typography.Text type="secondary">{t("learning.dictionaryMeaning")}</Typography.Text><Typography.Paragraph>{dictionaryMeaning}</Typography.Paragraph></section>:null}
      {fields.map(field=><section key={field.key}><Typography.Text type="secondary">{t(field.label_i18n_key)}</Typography.Text>{displayValue(resolvedValue[field.key])}</section>)}
    </div>}</Spin>
  </Modal>
}
