import {useEffect, useRef, useState} from "react";

interface Preferences {zoom: number}
const defaults: Preferences = {zoom:1};
function read(key?: string): Preferences {
  try {
    const value = key ? JSON.parse(sessionStorage.getItem(key) || "null") : null;
    return value && Number.isFinite(value.zoom) && value.zoom >= .5 && value.zoom <= 2 ? {zoom:value.zoom} : defaults;
  } catch { return defaults; }
}

/** Source hashes prevent preferences leaking between revisions of the same paper. */
export function usePdfReaderPreferences(key?: string, source?: unknown, scope?: string) {
  const [preferences,setPreferences] = useState<Preferences>(()=>read(key));
  const identity = useRef({key,source,scope});
  const skipWrite = useRef(false);
  const edited = useRef({zoom:false});
  useEffect(()=>{
    const previous = identity.current;
    if (previous.key === key && previous.source === source && previous.scope === scope) return;
    identity.current = {key,source,scope};
    skipWrite.current = true;
    const saved = read(key);
    if (!previous.key && key && previous.scope === scope) {
      // Fingerprinting is asynchronous: restore cached settings without undoing
      // individual choices the reader has already made while waiting.
      const changed = edited.current;
      setPreferences(current=>({
        zoom:changed.zoom ? current.zoom : saved.zoom,
      }));
      return;
    }
    edited.current = {zoom:false};
    setPreferences(saved);
  },[key,source,scope]);
  useEffect(()=>{
    if (skipWrite.current) { skipWrite.current=false; return; }
    if (!key) return;
    try { sessionStorage.setItem(key,JSON.stringify(preferences)); } catch { /* Reading remains available when storage is blocked. */ }
  },[key,preferences]);
  return {
    zoom:preferences.zoom,
    setZoom:(zoom:number)=>{edited.current.zoom=true;setPreferences(value=>({...value,zoom}));},
  };
}
