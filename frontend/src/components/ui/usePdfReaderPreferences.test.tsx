import {act,renderHook} from '@testing-library/react';
import {beforeEach,expect,it} from 'vitest';
import {usePdfReaderPreferences} from './usePdfReaderPreferences';

beforeEach(()=>sessionStorage.clear());

it('hydrates saved preferences when the asynchronously computed source hash arrives',()=>{
 const key='pdf-reader:dataset:paper:hash';
 sessionStorage.setItem(key,JSON.stringify({mode:'bilingual',zoom:1.5}));
 const source=new ArrayBuffer(4);
 const {result,rerender}=renderHook(({storageKey,bytes})=>usePdfReaderPreferences(storageKey,bytes,'dataset:paper'),{initialProps:{storageKey:undefined as string|undefined,bytes:source}});
 expect(result.current.readerMode).toBe('original');
 rerender({storageKey:key,bytes:new ArrayBuffer(4)});
 expect(result.current.readerMode).toBe('original');
 expect(result.current.zoom).toBe(1.5);
 expect(JSON.parse(sessionStorage.getItem(key)!)).toEqual({mode:'original',zoom:1.5});
});

it('preserves explicit choices made while waiting for the hash',()=>{
 const key='pdf-reader:dataset:paper:hash';
 sessionStorage.setItem(key,JSON.stringify({mode:'bilingual',zoom:1.5}));
 const {result,rerender}=renderHook(({storageKey})=>usePdfReaderPreferences(storageKey,'paper.pdf','dataset:paper'),{initialProps:{storageKey:undefined as string|undefined}});
 act(()=>{result.current.setReaderMode('insight');result.current.setZoom(1.2);});
 rerender({storageKey:key});
 expect(result.current.readerMode).toBe('insight');
 expect(result.current.zoom).toBe(1.2);
 expect(JSON.parse(sessionStorage.getItem(key)!)).toEqual({mode:'insight',zoom:1.2});
});

it('hydrates untouched settings while retaining the explicitly changed setting',()=>{
 const key='pdf-reader:dataset:paper:hash';
 sessionStorage.setItem(key,JSON.stringify({mode:'bilingual',zoom:1.5}));
 const {result,rerender}=renderHook(({storageKey})=>usePdfReaderPreferences(storageKey,'paper.pdf','dataset:paper'),{initialProps:{storageKey:undefined as string|undefined}});
 act(()=>result.current.setReaderMode('insight'));
 rerender({storageKey:key});
 expect(result.current.readerMode).toBe('insight');
 expect(result.current.zoom).toBe(1.5);
});
