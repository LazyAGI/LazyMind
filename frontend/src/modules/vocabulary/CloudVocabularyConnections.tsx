import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Checkbox, Form, Input, Modal, Popconfirm, Select, Skeleton, Space, Switch, Tag, Typography } from "antd";
import { ArrowRightOutlined, BookOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { createCloudWordbook, disconnectCloudConnection, listCloudConnections, listCloudOperations, saveCloudConnection, syncCloudVocabulary, testCloudConnection, type CloudConnection, type CloudOperation, type CloudVocabularyProvider, type CloudWordbook } from "./cloudApi";
import "./CloudVocabularyConnections.scss";
import { cloudErrorMessage } from "./cloudErrorMessage";
const providers: CloudVocabularyProvider[] = ["eudic", "maimemo"];
const emptyConnection = (provider: CloudVocabularyProvider): CloudConnection => ({provider, configured:false, enabled:false, target_id:"", target_name:"", add_to_study:false, pending:0, failed:0, succeeded:0, last_error:""});

// Counts and operation errors do not invalidate configuration drafts.
const configSignature = (connection: CloudConnection) => JSON.stringify([
  connection.configured, connection.enabled, connection.target_id,
  connection.target_name, connection.add_to_study,
]);

function ConnectionCard({ connection, operations, onSaved }: {connection: CloudConnection; operations: CloudOperation[]; onSaved:(connection:CloudConnection)=>void}) {
  const {t}=useTranslation();
  const label=(key:string)=>t(`vocabularyCloud.${key}`);
  const [token,setToken]=useState("");
  const [verifiedToken,setVerifiedToken]=useState<string | null>(connection.configured ? "" : null);
  const [study,setStudy]=useState(connection.add_to_study);
  const [target,setTarget]=useState<CloudWordbook | null>(connection.target_id ? {id:connection.target_id,name:connection.target_name} : null);
  const [books,setBooks]=useState<CloudWordbook[]>(target ? [target] : []);
  const [name,setName]=useState("LazyMind 生词本");
  const [busy,setBusy]=useState(false);
  const [conflict,setConflict]=useState(false);
  const [notice,setNotice]=useState("");
  const [error,setError]=useState(false);
  const baseline=useRef(connection);
  const translateRef=useRef(t);
  const mounted=useRef(true);
  const locked=useRef(false);
  const requestSequence=useRef(0);
  const connected=connection.configured && Boolean(connection.target_id);
  const verified=verifiedToken!==null && verifiedToken===token.trim();
  const dirty=Boolean(token) || study!==baseline.current.add_to_study || (target?.id || "")!==baseline.current.target_id || (target?.name || "")!==baseline.current.target_name || name!=="LazyMind 生词本";
  const resetDraft=useCallback((next:CloudConnection)=>{
    requestSequence.current+=1;
    baseline.current=next;
    setToken("");setVerifiedToken(next.configured ? "" : null);setStudy(next.add_to_study);
    const selected=next.target_id ? {id:next.target_id,name:next.target_name} : null;
    setTarget(selected);setBooks(current=>selected ? [...current.filter(item=>item.id!==selected.id),selected] : current);
    setName("LazyMind 生词本");setConflict(false);
  },[]);
  useEffect(()=>{
    if(conflict || configSignature(connection)===configSignature(baseline.current))return;
    if(dirty)setConflict(true);else resetDraft(connection);
  },[connection,conflict,dirty,resetDraft]);
  useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;};},[]);
  const run=async(action:()=>Promise<void>)=>{
    if(locked.current || conflict)return;
    locked.current=true;setBusy(true);setNotice("");setError(false);
    try{await action();}catch{if(mounted.current){setError(true);setNotice(label("actionFailed"));}}
    finally{locked.current=false;if(mounted.current)setBusy(false);}
  };
  const loadBooks=useCallback(async(provider:CloudVocabularyProvider,credential:string,selectedId:string)=>{
    const generation=++requestSequence.current;
    const items=await testCloudConnection(provider,credential || undefined);
    if(!mounted.current || generation!==requestSequence.current)return;
    setBooks(items);setVerifiedToken(credential);
    setTarget(items.find(item=>item.id===selectedId) || (items.length===1 ? items[0] : null));
  },[]);
  // Opening saved credentials only reads wordbooks; it never writes a connection or creates a target.
  useEffect(()=>{
    if(!baseline.current.configured)return;
    let cancelled=false;
    locked.current=true;setBusy(true);
    void loadBooks(baseline.current.provider,"",baseline.current.target_id)
      .catch(()=>{if(!cancelled && mounted.current){setError(true);setNotice(translateRef.current("vocabularyCloud.actionFailed"));}})
      .finally(()=>{if(cancelled)return;locked.current=false;if(mounted.current)setBusy(false);});
    return()=>{cancelled=true;requestSequence.current+=1;};
  },[loadBooks]);
  const persist=(nextEnabled:boolean,nextStudy=study,usePersisted=false)=>run(async()=>{
    if(configSignature(connection)!==configSignature(baseline.current) || !target || !verified)return;
    const saved=await saveCloudConnection(connection.provider,{...(!usePersisted && token.trim()?{token:token.trim()}:{}),enabled:nextEnabled,target_id:usePersisted ? connection.target_id : target.id,target_name:usePersisted ? connection.target_name : target.name,add_to_study:connection.provider==="maimemo" && nextStudy});
    if(!mounted.current)return;
    resetDraft(saved);onSaved(saved);setNotice(label(saved.enabled ? "savedEnabled" : "savedPaused"));
  });
  const tokenForm=<>
    <Typography.Paragraph type="secondary">{connection.provider==="eudic" ? <a href="https://my.eudic.net/OpenAPI/Authorization" target="_blank" rel="noopener noreferrer">{label("eudicTokenLink")}</a> : <>{label("maimemoTokenHelp")} <a href="https://open.maimemo.com/" target="_blank" rel="noopener noreferrer">{label("maimemoDocs")}</a></>}</Typography.Paragraph>
    <Form.Item label={label("token")}>
      <Input.Password aria-label={label("token")} autoComplete="new-password" value={token} onChange={e=>{setToken(e.target.value);setVerifiedToken(null);setTarget(null);setBooks([]);}}/>
    </Form.Item>
    <Button type={verified?"default":"primary"} disabled={busy || conflict || (!connection.configured && !token.trim())} onClick={()=>void run(()=>loadBooks(connection.provider,token.trim(),target?.id || ""))}>{label("verify")}</Button>
  </>;
  const showSave=!connected || dirty;
  return <section aria-label={label(connection.provider)}>
    <Tag color={connected && connection.enabled ? "green" : "default"}>{label(connected ? connection.enabled ? "connectedEnabled" : "connectedPaused" : "incomplete")}</Tag>
    {conflict && <Alert type="warning" showIcon message={label("configConflict")} action={<Button disabled={busy} onClick={()=>resetDraft(connection)}>{label("reloadSettings")}</Button>}/>}
    <Form layout="vertical" disabled={busy || conflict}>
      {!connection.configured && tokenForm}
      {busy && <Typography.Paragraph type="secondary">{label("loading")}</Typography.Paragraph>}
      {verified && <>
        <Form.Item label={label("target")}>
          <Select aria-label={label("target")} value={target?.id} placeholder={label("selectTarget")} options={books.map(item=>({value:item.id,label:item.name}))} onChange={id=>setTarget(books.find(item=>item.id===id)||null)}/>
        </Form.Item>
        {connected && <Form.Item style={{marginTop:16}}><Space><Switch aria-label={label("autoSync")} checked={connection.enabled} disabled={busy || conflict || dirty || !target || !verified} onChange={value=>void persist(value,connection.add_to_study,true)}/><span>{label("autoSync")}</span></Space></Form.Item>}
        {!books.length && !busy && <Typography.Paragraph type="secondary">{label("noBooks")}</Typography.Paragraph>}
        <details className="vocabulary-cloud-details"><summary>{label("createTarget")}</summary>
          <Space.Compact style={{width:"100%"}}><Input aria-label={label("newTarget")} value={name} onChange={e=>setName(e.target.value)}/><Button disabled={busy || conflict || !name.trim()} onClick={()=>void run(async()=>{
            const book=await createCloudWordbook(connection.provider,name.trim(),token.trim()||undefined);if(!mounted.current)return;
            setBooks(current=>[...current.filter(item=>item.id!==book.id),book]);setTarget(book);setName("LazyMind 生词本");
          })}>{label("create")}</Button></Space.Compact>
        </details>
        {showSave && <Button type="primary" aria-label={label(connected?"save":"saveAndEnable")} loading={busy} disabled={busy || conflict || !target} onClick={()=>void persist(connected ? connection.enabled : true)}>{label(connected?"save":"saveAndEnable")}</Button>}
      </>}
      <details className="vocabulary-cloud-details"><summary>{label("advanced")}</summary>
        {connection.configured && <details className="vocabulary-cloud-details"><summary>{label("changeToken")}</summary>{tokenForm}</details>}
        {connection.provider==="maimemo" && <Form.Item help={label("studyHelp")}><Checkbox checked={study} disabled={busy || conflict || (connected && dirty)} onChange={e=>{if(connected)void persist(connection.enabled,e.target.checked,true);else setStudy(e.target.checked);}}>{label("study")}</Checkbox></Form.Item>}
        <Space wrap>
          <Button disabled={busy || conflict || !connected || !connection.enabled} onClick={()=>void run(async()=>{const result=await syncCloudVocabulary(connection.provider);if(mounted.current)setNotice(t("vocabularyCloud.queued",{count:result.queued}));})}>{label("sync")}</Button>
          <Popconfirm title={label("disconnectConfirm")} onConfirm={()=>run(async()=>{await disconnectCloudConnection(connection.provider);if(!mounted.current)return;const empty=emptyConnection(connection.provider);resetDraft(empty);setBooks([]);onSaved(empty);setNotice(label("disconnected"));})}><Button danger disabled={busy || conflict || !connection.configured}>{label("disconnect")}</Button></Popconfirm>
        </Space>
      </details>
    </Form>
    {notice && <Alert type={error?"error":"success"} showIcon message={notice}/>}
    <div className="vocabulary-cloud-stats"><Tag>{label("pending")}: {connection.pending}</Tag><Tag color={connection.failed?"error":"default"}>{label("failed")}: {connection.failed}</Tag><Tag color="success">{label("succeeded")}: {connection.succeeded}</Tag></div>
    {connection.last_error && <Alert type="warning" message={cloudErrorMessage(connection.last_error,t)} showIcon/>}
    {operations.length>0 && <div className="vocabulary-cloud-operations"><Typography.Text type="secondary">{label("recent")}</Typography.Text><ul>{operations.slice(0,10).map(operation=><li key={operation.id}><Space wrap><strong>{operation.term}</strong><Tag>{t(`vocabularyCloud.status.${operation.status}`,{defaultValue:operation.status})}</Tag><small>{new Date(operation.updated_at).toLocaleString()}</small></Space>{operation.last_error && <div>{cloudErrorMessage(operation.last_error,t)}</div>}</li>)}</ul></div>}
  </section>;
}
export default function CloudVocabularyConnections({onReadyCountChange}: {onReadyCountChange?: (count:number)=>void}) {
  const {t}=useTranslation();
  const [connections,setConnections]=useState<CloudConnection[]>([]);
  const [operations,setOperations]=useState<CloudOperation[]>([]);
  const [loaded,setLoaded]=useState(false);
  const [error,setError]=useState(false);
  const [reload,setReload]=useState(0);
  const [selected,setSelected]=useState<CloudVocabularyProvider | null>(null);
  const polling = useRef(false);
  const configRevision = useRef(0);
  useEffect(()=>{
    let disposed=false;
    let timer:ReturnType<typeof setTimeout>;
    const poll=async()=>{
      if(polling.current){timer=setTimeout(()=>void poll(),10000);return;}
      polling.current=true;
      const revision=configRevision.current;
      try {
        const [items,recent]=await Promise.all([listCloudConnections(),listCloudOperations()]);
        if(!disposed){if(revision===configRevision.current)setConnections(items);setOperations(recent);setLoaded(true);setError(false);}
      } catch {if(!disposed)setError(true);}
      finally {polling.current=false;if(!disposed)timer=setTimeout(()=>void poll(),10000);}
    };
    void poll();return()=>{disposed=true;clearTimeout(timer);};
  },[reload]);
  useEffect(()=>{onReadyCountChange?.(connections.filter(item=>item.configured && item.enabled).length);},[connections,onReadyCountChange]);
  const selectedConnection=selected ? connections.find(item=>item.provider===selected)||emptyConnection(selected) : null;
  return <>
    {providers.map(provider=>{
      const connection=connections.find(item=>item.provider===provider);
      const status=error ? "unavailable" : !loaded ? "loading" : connection?.target_id ? connection.enabled ? "connectedEnabled" : "connectedPaused" : "incomplete";
      return <article key={provider} className="model-provider-cloud-doc-resource-row">
        <span className="model-provider-cloud-doc-resource-logo"><BookOutlined/></span>
        <div className="model-provider-cloud-doc-resource-copy">
          <h3>{t(`vocabularyCloud.${provider}`)}</h3>
          <p>{t("vocabularyCloud.providerDescription")}</p>
        </div>
        <Tag className="model-provider-cloud-doc-resource-status" color={error ? "warning" : connection?.enabled ? "success" : "default"}>{t(`vocabularyCloud.${status}`)}</Tag>
        <div className="model-provider-cloud-doc-resource-controls">
          <button type="button" className="model-provider-cloud-doc-resource-action" aria-label={`${t(`vocabularyCloud.${provider}`)} ${t("vocabularyCloud.manage")}`} onClick={()=>setSelected(provider)}>
            {t("vocabularyCloud.manage")}<ArrowRightOutlined/>
          </button>
        </div>
      </article>;
    })}
    <Modal open={Boolean(selected)} title={selected ? t(`vocabularyCloud.${selected}`) : ""} onCancel={()=>setSelected(null)} footer={null} width={680} destroyOnHidden styles={{body:{maxHeight:"75vh",overflowY:"auto"}}}>
      <Typography.Paragraph type="secondary">{t("vocabularyCloud.connectionIntro")}</Typography.Paragraph>
      {error && <Alert type="warning" showIcon message={t("vocabularyCloud.loadFailed")} action={<Button onClick={()=>setReload(value=>value+1)}>{t("vocabularyCloud.refresh")}</Button>}/>}
      {!loaded ? (!error && <Skeleton active/>) : selectedConnection && <ConnectionCard key={selectedConnection.provider} connection={selectedConnection} operations={operations.filter(item=>item.provider===selected)} onSaved={saved=>{configRevision.current+=1;setConnections(current=>[...current.filter(item=>item.provider!==selected),saved]);}}/>}
    </Modal>
  </>;
}
