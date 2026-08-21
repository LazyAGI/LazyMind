/**
 * Server-Sent Events (SSE) is a standard describing how servers can initiate
 * data transmission towards browser clients once an initial client connection
 * has been established.
 * Native SSE does not support POST method.
 */

import { timer, Subscription } from "rxjs";

/** Default timeout for the SSE connection. */
const DEFAULT_TIMEOUT = 2000;

/** Enum representing the states of the XHR connection. */
enum XHRStates {
  /** The connection is initializing. */
  INITIALIZING = -1,
  /** The connection is connecting. */
  CONNECTING = 0,
  /** The connection is open. */
  OPEN = 1,
  /** The connection is closed. */
  CLOSED = 2,
}

/** Enum representing the HTTP methods. */
enum Method {
  GET = "GET",
  POST = "POST",
}

enum TriggerEvent {
  OPEN = "open",
  PROGRESS = "progress",
  READYSTATECHANGE = "readystatechange",
  ERROR = "error",
  ABORT = "abort",
  LOAD = "load",
  TIMEOUT = "timeout",
}

/** Options for the SSE connection. */
interface SSEOptions {
  /** The headers to be sent with the request. */
  headers?: Record<string, string>;
  /** The timeout for the connection. */
  timeout?: number;
  /** The payload to be sent with the request. */
  payload?: string;
  /** The HTTP method to be used. */
  method?: Method;
  /** Whether to send credentials with the request. */
  withCredentials?: boolean;
  /** Whether to log debug information. */
  debug?: boolean;
  /** Callbacks for the different events. */
  callbacks?: Record<string, (e: CustomEvent) => void>;
  /** Whether to start the connection immediately. */
  start?: boolean;
  /** Whether to start the connection manually. */
  manual?: boolean;
}

/** Data for the event. */
interface EventData {
  id: unknown;
  retry: unknown;
  data: string | null;
  event: string;
}

/** Custom event types. */
type CustomEventReadyStateChangeType = CustomEvent & {
  /** The ready state of the XHR connection. */
  readyState: XHRStates;
};

/** Custom error event types. */
type CustomEventErrorType = CustomEvent & {
  data: unknown;
  status: number;
};

/** Custom data event types. */
type CustomEventDataType = CustomEvent & {
  data: unknown;
  id: string;
};

/** Custom event types. */
type CustomEventType =
  | CustomEvent
  | CustomEventDataType
  | CustomEventReadyStateChangeType
  | CustomEventErrorType;

/** Callback type for the event listeners. */
type Callback = (e: CustomEventType) => void;

/**
 * Server-Sent Events (SSE) class.
 * @example const sse = new SSE('http://example.com/stream', options);
 * @example options = {headers: {}, payload: '', method: 'GET', ...};
 * @example sse.addEventListener('open', (e) => console.log('open', e));
 */
class SSE {
  /** The field separator for the event data. */
  private FIELD_SEPARATOR = ":";

  /** The connection is initializing. */
  private INITIALIZING = -1;

  /** The connection is connecting. */
  private CONNECTING = 0;

  /** The connection is open. */
  private OPEN = 1;

  /** The connection is closed. */
  private CLOSED = 2;

  /** The timeout for the connection. */
  private TIMEOUT = 5;

  /** The URL for the connection. */
  public url: string;

  /** The headers to be sent with the request. */
  public headers: Record<string, string>;

  /** The timeout for the connection. */
  public timeout: number;

  /** The payload to be sent with the request. */
  public payload: string;

  /** The HTTP method to be used. */
  public method: Method;

  /** Whether to send credentials with the request. */
  public withCredentials: boolean;

  /** Whether to log debug information. */
  public debug: boolean;

  /** The event listeners. */
  public listeners: Record<string, Callback[]> = {};

  /** The XHR connection. */
  public xhr: XMLHttpRequest | null = null;

  /** The ready state of the XHR connection. */
  public readyState: number;

  /** The progress of the XHR connection. */
  public progress = 0;

  /** The chunk of data received from the XHR connection. */
  public chunk = "";

  /** The subscription for the timeout. */
  public timeoutSubscription: Subscription | null = null;

  /**
   * Create a new SSE connection.
   * @param url The URL for the connection.
   * @param options The options for the connection.
   */

  /** The constructor for the SSE connection. */
  constructor(url: string, options: SSEOptions = {}) {
    this.url = url;
    this.headers = options.headers || {};
    this.timeout = options.timeout || DEFAULT_TIMEOUT;
    this.payload = options.payload ?? "";
    this.method = options.method || (this.payload && Method.POST) || Method.GET;
    this.withCredentials = !!options.withCredentials;
    this.debug = !!options.debug;
    this.readyState = this.INITIALIZING;

    /** Add the event listeners from the options. */
    Object.entries(options.callbacks || {}).forEach(([key, value]) => {
      (this as unknown as Record<string, (event: CustomEvent) => void>)[
        `on${key}`
      ] = value;
    });

    /** Start the connection if the options allow. */
    if ((options.start === undefined || options.start) && !options.manual) {
      this.stream();
    }
  }

  /** Set the timeout for the connection. */
  private setTimeoutFun(): void {
    if (!this.xhr) {
      return;
    }

    this.setReadyState(this.TIMEOUT);
    this.onStreamTimeout();
  }

  /** Reset the timeout for the connection. */
  private resetTimeout(): void {
    /**
     * Cancel the timeout subscription.
     * If not canceled, multiple subscriptions will cause multiple timeout
     * events to be triggered.
     */
    this.timeoutSubscription?.unsubscribe();

    /** Create a new timeout subscription. */
    this.timeoutSubscription = timer(this.timeout).subscribe(() => {
      this.setTimeoutFun();
    });
  }

  /**
   * Add an event listener.
   * @param type The type of the event.
   * @param listener The callback for the event.
   */
  public addEventListener(type: string, listener: Callback): void {
    /** If the event listener does not exist, create it. */
    this.listeners[type] = this.listeners[type] || [];

    /** If the event listener does not exist, add it. */
    if (this.listeners[type]?.indexOf(listener) === -1) {
      this.listeners[type]?.push(listener);
    }
  }

  /**
   * Remove an event listener.
   * @param type The type of the event.
   * @param listener The callback for the event.
   */
  public removeEventListener(type: string, listener: Callback): void {
    /** If the event listener does not exist, return. */
    if (!this.listeners[type]) {
      return;
    }

    /** Filter the event listeners. */
    const filteredListeners =
      this.listeners[type]?.filter((lis) => lis !== listener) ?? [];

    /** If the event listeners are empty, delete the event listener. */
    if (filteredListeners.length === 0) {
      delete this.listeners[type];
    } else {
      this.listeners[type] = filteredListeners;
    }
  }

  /**
   * Dispatch an event.
   * @param e The event to dispatch.
   * @returns Whether the event was dispatched.
   */
  public dispatchEvent(e: CustomEvent): boolean {
    /** If the event does not exist, return. */
    if (!e) {
      return true;
    }

    /** Log the event if debug is enabled. */
    if (this.debug) {
      console.debug(e);
    }

    /** Call the event handler if it exists. */
    const onHandler = `on${e.type}`;
    if (Object.prototype.hasOwnProperty.call(this, onHandler)) {
      (this as unknown as Record<string, Callback>)[onHandler]?.call(this, e);

      /** If the event was prevented, return false. */
      if (e.defaultPrevented) {
        return false;
      }
    }

    /** If the event listener does not exist, return true. */
    if (this.listeners[e.type]) {
      return (
        this.listeners[e.type]?.every((callback) => {
          callback(e);
          return !e.defaultPrevented;
        }) ?? false
      );
    }

    return true;
  }

  /**
   * Set the ready state of the XHR connection.
   * @param state The ready state of the XHR connection.
   */
  private setReadyState(state: XHRStates): void {
    /**
     * Create a new custom event for the ready state change.
     */
    const event = new CustomEvent(
      TriggerEvent.READYSTATECHANGE,
    ) as CustomEventReadyStateChangeType;

    /** Set the ready state of the XHR connection. */
    event.readyState = state;
    this.readyState = state;

    /** Dispatch the event. */
    this.dispatchEvent(event);
  }

  /**
   * Handle the failure of the XHR connection.
   * @param e The event for the failure.
   */
  private onStreamFailure(e: Event): void {
    /** Create a new custom event for the error. */
    const event = new CustomEvent(TriggerEvent.ERROR) as CustomEventErrorType;

    /** Set the data for the error. */
    const xhr = e.currentTarget as XMLHttpRequest;
    event.data = xhr?.response;
    event.status = Number(xhr?.status || this.xhr?.status || 0);

    /** Dispatch the event. */
    this.dispatchEvent(event);

    /** Close the connection. */
    this.close();
  }

  /**
   * Handle the timeout of the XHR connection.
   */
  private onStreamTimeout(): void {
    const event = new CustomEvent(TriggerEvent.TIMEOUT);
    this.dispatchEvent(event);
    this.close();
  }

  /**
   * Handle the abort of the XHR connection.
   */
  private onStreamAbort(): void {
    this.dispatchEvent(new CustomEvent(TriggerEvent.ABORT));
    this.close();
  }

  /**
   * Handle the progress of the XHR connection.
   * The chunk of data received from the XHR connection.
   * @param e The event for the progress.
   */
  private onStreamProgress(e: ProgressEvent): void {
    /** If the XHR connection does not exist, return. */
    if (!this.xhr) {
      return;
    }

    /**
     * If the status of the XHR connection is not 200,
     *  handle the failure.
     */
    if (this.xhr.status !== 200) {
      this.onStreamFailure(e);
      return;
    }

    /**
     * If the ready state of the XHR connection is connecting, set the ready
     * state to open.
     */
    if (this.readyState === this.CONNECTING) {
      this.dispatchEvent(new CustomEvent(TriggerEvent.OPEN));
      this.setReadyState(this.OPEN);
    }

    /** Get the data from the XHR connection. */
    const data = this.xhr.responseText.substring(this.progress);
    this.progress += data.length;

    /** Split the data into parts. */
    const parts = (this.chunk + data).split(/(\r\n|\r|\n){2}/g);

    /** Get the last part of the data. */
    const lastPart = parts.pop() || "";

    /** Dispatch the event for each part. */
    parts.forEach((part) => {
      if (part.trim().length > 0) {
        this.dispatchEvent(this.parseEventChunk(part) as CustomEventType);
      }
    });

    /** Reset the timeout for the connection. */
    this.resetTimeout();

    /** Set the chunk of data. */
    this.chunk = lastPart;
  }

  /**
   * Handle the loaded stream.
   * @param e The event for the loaded stream.
   */
  private onStreamLoaded(e: ProgressEvent): void {
    this.onStreamProgress(e);
    this.dispatchEvent(this.parseEventChunk(this.chunk) as CustomEventType);
    this.chunk = "";
  }

  /**
   * Parse the event chunk.
   * @param chunk The chunk of data to parse.
   * @returns The parsed event.
   */
  private parseEventChunk(chunk: string): CustomEventType | null {
    /** If the chunk does not exist or is empty, return null. */
    if (!chunk || chunk.length === 0) {
      return null;
    }

    /** Log the chunk if debug is enabled. */
    if (this.debug) {
      console.debug(chunk);
    }

    /** Create a new event data object. */
    const eventData: EventData = {
      id: null,
      retry: null,
      data: null,
      event: "message",
    };

    /** Split the chunk into lines. */
    chunk.split(/\n|\r\n|\r/).forEach((line) => {
      /** Trim the line. */
      line = line.trimEnd();

      /** If the line is empty, return. */
      const index = line.indexOf(this.FIELD_SEPARATOR);
      if (index === 0) {
        return;
      }

      /**
       * Get the field and value from the line.
       * Get the value from the line.
       */
      let field = "",
        value = "";
      if (index > 0) {
        const skip = line[index + 1] === " " ? 2 : 1;
        field = line.substring(0, index);
        value = line.substring(index + skip);
      } else {
        field = line;
        value = "";
      }

      if (!(field in eventData)) {
        return;
      }

      /** Set the value for the field. */
      if (field === "data" && eventData[field] !== null) {
        eventData.data += "\n" + value;
      } else {
        eventData[field as keyof EventData] = value;
      }
    });

    /** Create a new custom event for the event data. */
    const event = new CustomEvent(eventData.event);
    (event as unknown as Record<string, unknown>).data = eventData.data || "";
    (event as unknown as Record<string, unknown>).id = eventData.id;

    return event;
  }

  /**
   * Check if the stream is closed.
   */
  private checkStreamClosed(): void {
    if (!this.xhr) {
      return;
    }
    if (this.xhr.readyState === XMLHttpRequest.DONE) {
      this.setReadyState(this.CLOSED);
    }
  }

  /** Start the connection. */
  public stream(): void {
    /** If the XHR connection exists, return. */
    if (this.xhr) {
      return;
    }

    /** Set the ready state to connecting. */
    this.setReadyState(this.CONNECTING);

    /** Create a new XHR connection. */
    this.xhr = new XMLHttpRequest();

    /** Add the event listeners to the XHR connection. */
    this.xhr.addEventListener(
      TriggerEvent.PROGRESS,
      this.onStreamProgress.bind(this),
    );
    this.xhr.addEventListener(
      TriggerEvent.LOAD,
      this.onStreamLoaded.bind(this),
    );
    this.xhr.addEventListener(
      TriggerEvent.READYSTATECHANGE,
      this.checkStreamClosed.bind(this),
    );
    this.xhr.addEventListener(
      TriggerEvent.ERROR,
      this.onStreamFailure.bind(this),
    );
    this.xhr.addEventListener(
      TriggerEvent.ABORT,
      this.onStreamAbort.bind(this),
    );

    /** Open the XHR connection. */
    this.xhr.open(this.method, this.url);
    for (const header in this.headers) {
      this.xhr.setRequestHeader(header, this.headers[header] as string);
    }

    /** Set the credentials for the XHR connection. */
    this.xhr.withCredentials = this.withCredentials;

    /** Send the payload with the XHR connection. */
    this.xhr.send(this.payload);

    /** Reset the timeout for the connection. */
    this.resetTimeout();
  }

  /** Close the connection. */
  public close(): void {
    /** Cancel the timeout subscription. */
    this.timeoutSubscription?.unsubscribe();
    this.timeoutSubscription = null;
    /** If the XHR connection does not exist, return. */
    if (this.readyState === this.CLOSED) {
      return;
    }

    /** Abort the XHR connection. */
    this.xhr?.abort();
    this.xhr = null;
    this.setReadyState(this.CLOSED);
  }
}

/** Export the types. */
export type {
  SSEOptions,
  EventData,
  CustomEventReadyStateChangeType,
  CustomEventErrorType,
  CustomEventDataType,
  CustomEventType,
  Callback,
};

/** Export the SSE class. */
export { SSE, Method, TriggerEvent, XHRStates };
