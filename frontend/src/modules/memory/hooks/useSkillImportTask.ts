import { useCallback, useEffect, useRef, useState } from "react";

interface SkillImportContext {
  signal: AbortSignal;
  setUploadProgress: (percent: number | null) => void;
}

export function useSkillImportTask<T>(callbacks: {
  onSuccess: (result: T) => void;
  onError: (error: unknown) => void;
}) {
  const callbacksRef = useRef(callbacks);
  callbacksRef.current = callbacks;
  const controllerRef = useRef<AbortController | null>(null);
  const [pending, setPending] = useState(false);
  const [uploadProgress, setUploadProgress] = useState<number | null>(null);

  const cancel = useCallback(() => {
    const controller = controllerRef.current;
    controllerRef.current = null;
    controller?.abort();
    setPending(false);
    setUploadProgress(null);
  }, []);

  useEffect(() => () => {
    const controller = controllerRef.current;
    controllerRef.current = null;
    controller?.abort();
  }, []);

  const start = useCallback(async (execute: (context: SkillImportContext) => Promise<T>) => {
    // A ref also guards multiple submissions before React renders pending state.
    if (controllerRef.current) return;
    const controller = new AbortController();
    controllerRef.current = controller;
    setPending(true);
    setUploadProgress(null);
    try {
      const result = await execute({
        signal: controller.signal,
        setUploadProgress: (percent) => {
          if (controllerRef.current === controller) setUploadProgress(percent);
        },
      });
      if (controllerRef.current !== controller || controller.signal.aborted) return;
      callbacksRef.current.onSuccess(result);
    } catch (error) {
      if (controllerRef.current === controller && !controller.signal.aborted) {
        callbacksRef.current.onError(error);
      }
    } finally {
      if (controllerRef.current === controller) {
        controllerRef.current = null;
        setPending(false);
        setUploadProgress(null);
      }
    }
  }, []);

  return { pending, uploadProgress, start, cancel };
}
