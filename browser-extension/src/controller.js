const CDP_VERSION = '1.3';
const MAX_SNAPSHOT_ELEMENTS = 500;
const INTERACTIVE_ROLES = new Set([
  'button', 'checkbox', 'combobox', 'link', 'listbox', 'menuitem', 'option',
  'radio', 'searchbox', 'slider', 'spinbutton', 'switch', 'tab', 'textbox',
  'treeitem', 'heading', 'StaticText',
]);
// Full-automation mode temporarily disables the local action safety policy.
// Keep the original rules here so they can be restored when an approval flow is available.
// const HIGH_RISK_NAME = /(提交|发送|发布|删除|移除|购买|付款|支付|转账|确认订单|授权|同意|submit|send|publish|delete|remove|buy|purchase|pay|transfer|authorize|approve)/i;
// const SENSITIVE_NAME = /(密码|口令|验证码|动态码|支付|银行卡|信用卡|身份证|password|passcode|otp|verification|credit card|card number|cvv|ssn)/i;
// const ALLOWED_KEYS = new Set([
//   'Escape', 'Tab', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight',
//   'Backspace', 'Delete',
// ]);

export class BrowserController {
  constructor() {
    this.sessions = new Map();
    chrome.debugger.onDetach.addListener((source) => this.handleDetach(source));
  }

  async dispatch(action, payload = {}) {
    switch (action) {
      case 'open': return this.open(payload);
      case 'navigate': return this.navigate(payload);
      case 'snapshot': return this.snapshot(this.requireSession(payload.session_id));
      case 'click': return this.click(payload);
      case 'click_intersection': return this.clickIntersection(payload);
      case 'type': return this.type(payload);
      case 'type_focused': return this.typeFocused(payload);
      case 'select': return this.select(payload);
      case 'press': return this.press(payload);
      case 'scroll': return this.scroll(payload);
      case 'wait': return this.wait(payload);
      case 'screenshot': return this.screenshot(payload);
      case 'tabs': return this.tabs(payload);
      case 'close': return this.close(payload);
      default: throw browserError('UNSUPPORTED_ACTION', `不支持浏览器动作 ${action}`);
    }
  }

  async open(payload) {
    const url = validateTargetURL(payload.url, payload.allow_private_network);
    const created = await chrome.windows.create({
      url,
      focused: true,
      type: 'normal',
      state: 'maximized',
    });
    const tab = created.tabs?.[0];
    if (!tab?.id) {
      if (created.id) await chrome.windows.remove(created.id);
      throw browserError('OPEN_FAILED', '浏览器没有创建受控标签页');
    }
    try {
      await chrome.debugger.attach({tabId: tab.id}, CDP_VERSION);
      await Promise.all([
        this.send(tab.id, 'Page.enable'),
        this.send(tab.id, 'DOM.enable'),
        this.send(tab.id, 'Runtime.enable'),
        this.send(tab.id, 'Accessibility.enable'),
      ]);
      await waitForTabComplete(tab.id, 15000);
      const session = {
        id: `bs_${crypto.randomUUID().replaceAll('-', '')}`,
        windowId: created.id,
        tabId: tab.id,
        revision: 0,
        refs: new Map(),
        detached: false,
      };
      this.sessions.set(session.id, session);
      const snapshot = await this.snapshot(session);
      return {session_id: session.id, ...snapshot};
    } catch (error) {
      try { await chrome.windows.remove(created.id); } catch {}
      throw normalizeError(error, 'OPEN_FAILED');
    }
  }

  async navigate(payload) {
    const session = this.requireSession(payload.session_id);
    const url = validateTargetURL(payload.url, payload.allow_private_network);
    await this.send(session.tabId, 'Page.navigate', {url});
    await waitForTabComplete(session.tabId, 15000);
    return this.snapshot(session);
  }

  async prepareVisiblePage(session) {
    // Virtualized editors may defer their DOM/AX updates while their window is
    // covered or inactive. CDP scroll offsets alone do not prove a new frame
    // has rendered. Activate only the session's managed tab before observing.
    const tab = await chrome.tabs.get(session.tabId);
    await chrome.windows.update(tab.windowId, {focused: true});
    await chrome.tabs.update(session.tabId, {active: true});
    await this.send(session.tabId, 'Page.bringToFront');
    await this.send(session.tabId, 'Runtime.evaluate', {
      expression: `new Promise(resolve => {
        const timer = setTimeout(() => resolve('timeout'), 500);
        requestAnimationFrame(() => requestAnimationFrame(() => {
          clearTimeout(timer); resolve('rendered');
        }));
      })`,
      awaitPromise: true,
      returnByValue: true,
    });
  }

  async snapshot(session) {
    this.ensureAttached(session);
    await this.prepareVisiblePage(session);
    const startedAt = Date.now();
    const [tree, tab, pageMetadata] = await Promise.all([
      this.send(session.tabId, 'Accessibility.getFullAXTree'),
      chrome.tabs.get(session.tabId),
      this.send(session.tabId, 'Runtime.evaluate', {
        expression: '({url: location.href, title: document.title})',
        returnByValue: true,
        silent: true,
      }),
    ]);
    session.revision += 1;
    session.refs = new Map();
    const elements = [];
    const nodes = tree.nodes || [];
    const metrics = {
      raw_ax_nodes: nodes.length,
      included_elements: 0,
      ignored_nodes: 0,
      missing_backend_node_id_nodes: 0,
      unsupported_role_nodes: 0,
      empty_text_nodes: 0,
      invisible_text_nodes: 0,
      role_counts: {},
      snapshot_ms: 0,
    };
    for (const node of nodes) {
      const role = String(node.role?.value || '');
      const backendNodeId = node.backendDOMNodeId;
      if (node.ignored) {
        metrics.ignored_nodes += 1;
        continue;
      }
      if (!backendNodeId) {
        metrics.missing_backend_node_id_nodes += 1;
        continue;
      }
      if (!INTERACTIVE_ROLES.has(role)) {
        metrics.unsupported_role_nodes += 1;
        continue;
      }
      const rawName = String(node.name?.value || '');
      const rawValue = String(node.value?.value || '');
      const name = normalizeText(rawName);
      const value = normalizeText(rawValue);
      if (!name && !value && role !== 'textbox') {
        metrics.empty_text_nodes += 1;
        if (isInvisibleOnlyText(rawName) || isInvisibleOnlyText(rawValue)) {
          metrics.invisible_text_nodes += 1;
        }
        continue;
      }
      const ref = `e_${backendNodeId}`;
      const properties = Object.fromEntries((node.properties || []).map((property) => [
        property.name,
        property.value?.value,
      ]));
      // Full-automation mode returns and accepts every field value.
      // const sensitive = Boolean(properties.protected) || SENSITIVE_NAME.test(name);
      const sensitive = false;
      const record = {backendNodeId, role, name, sensitive};
      session.refs.set(ref, record);
      elements.push({
        ref, role, name, value: sensitive ? '' : value,
        state: Object.entries(properties)
          .filter(([, propertyValue]) => typeof propertyValue === 'boolean' && propertyValue)
          .map(([propertyName]) => propertyName),
        sensitive,
      });
      metrics.role_counts[role] = (metrics.role_counts[role] || 0) + 1;
      if (elements.length >= MAX_SNAPSHOT_ELEMENTS) break;
    }
    metrics.included_elements = elements.length;
    metrics.snapshot_ms = Date.now() - startedAt;
    const metadata = pageMetadata.result?.value || {};
    const result = {
      untrusted_browser_content: true,
      session_id: session.id,
      revision: session.revision,
      url: metadata.url || tab.url || '',
      title: metadata.title || tab.title || '',
      elements,
      limitations: elements.length >= MAX_SNAPSHOT_ELEMENTS ? ['snapshot_element_limit_reached'] : [],
      snapshot_metrics: metrics,
    };
    const responseBytes = new TextEncoder().encode(JSON.stringify(result)).byteLength;
    console.info('[LazyMind Browser] snapshot', {
      session_id: session.id,
      revision: session.revision,
      response_bytes: responseBytes,
      ...metrics,
    });
    return result;
  }

  async click(payload) {
    const session = this.requireSession(payload.session_id);
    const target = this.resolveRef(session, payload.ref, payload.expected_revision);
    // Full-automation mode: allow consequential controls such as send and submit.
    // if (HIGH_RISK_NAME.test(target.name)) {
    //   throw browserError(
    //     'HUMAN_TAKEOVER_REQUIRED',
    //     `“${target.name || target.role}”可能产生外部或不可逆影响，请用户手工完成。`,
    //   );
    // }
    const rect = await this.elementRect(session, target);
    const x = rect.x + rect.width / 2;
    const y = rect.y + rect.height / 2;
    await this.send(session.tabId, 'Input.dispatchMouseEvent', {type: 'mouseMoved', x, y});
    await this.send(session.tabId, 'Input.dispatchMouseEvent', {type: 'mousePressed', x, y, button: 'left', clickCount: 1});
    await this.send(session.tabId, 'Input.dispatchMouseEvent', {type: 'mouseReleased', x, y, button: 'left', clickCount: 1});
    await delay(300);
    return this.snapshot(session);
  }

  async clickIntersection(payload) {
    const session = this.requireSession(payload.session_id);
    const rowTarget = this.resolveRef(session, payload.row_ref, payload.expected_revision);
    const columnTarget = this.resolveRef(session, payload.column_ref, payload.expected_revision);
    if (rowTarget.backendNodeId === columnTarget.backendNodeId) {
      throw browserError('INVALID_INTERSECTION', '行标签和列标题必须是两个不同的元素引用');
    }

    await this.elementRect(session, columnTarget);
    await this.elementRect(session, rowTarget);
    const rowRect = await this.elementRect(session, rowTarget, {scroll: false});
    const columnRect = await this.elementRect(session, columnTarget, {scroll: false});
    const viewport = await this.viewportSize(session);
    const point = intersectionPoint(rowRect, columnRect, viewport);
    const before = await this.inspectPoint(session, point);
    await this.dispatchClick(session.tabId, point.x, point.y);
    await delay(300);
    const after = await this.inspectPoint(session, point);
    const snapshot = await this.snapshot(session);
    return {
      ...snapshot,
      interaction: {
        kind: 'intersection_click',
        row: {ref: payload.row_ref, name: rowTarget.name, rect: rowRect},
        column: {ref: payload.column_ref, name: columnTarget.name, rect: columnRect},
        point,
        before,
        after,
      },
    };
  }

  async type(payload) {
    const session = this.requireSession(payload.session_id);
    const target = this.resolveRef(session, payload.ref, payload.expected_revision);
    // Full-automation mode: allow sensitive-field input.
    // if (target.sensitive) {
    //   throw browserError('HUMAN_TAKEOVER_REQUIRED', '密码、验证码、支付或身份字段必须由用户手工输入。');
    // }
    await this.send(session.tabId, 'DOM.focus', {backendNodeId: target.backendNodeId});
    if (payload.replace) {
      const objectId = await this.resolveObject(session.tabId, target.backendNodeId);
      await this.send(session.tabId, 'Runtime.callFunctionOn', {
        objectId,
        functionDeclaration: `function () {
          if (!('value' in this)) return;
          const prototype = Object.getPrototypeOf(this);
          const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set;
          if (setter) setter.call(this, ''); else this.value = '';
          this.dispatchEvent(new Event('input', {bubbles: true}));
          this.dispatchEvent(new Event('change', {bubbles: true}));
        }`,
        returnByValue: true,
      });
    }
    await this.send(session.tabId, 'Input.insertText', {text: String(payload.text || '')});
    await delay(150);
    await this.verifyVisibleText(session, payload.verify_text);
    return this.snapshot(session);
  }

  async typeFocused(payload) {
    const session = this.requireSession(payload.session_id);
    if (payload.replace) {
      await this.clearFocusedElement(session);
    }
    await this.send(session.tabId, 'Input.insertText', {text: String(payload.text || '')});
    await delay(150);
    await this.verifyVisibleText(session, payload.verify_text);
    return this.snapshot(session);
  }

  async select(payload) {
    const session = this.requireSession(payload.session_id);
    const target = this.resolveRef(session, payload.ref, payload.expected_revision);
    const objectId = await this.resolveObject(session.tabId, target.backendNodeId);
    const response = await this.send(session.tabId, 'Runtime.callFunctionOn', {
      objectId,
      functionDeclaration: `function (value) {
        if (!(this instanceof HTMLSelectElement)) return {ok: false};
        this.value = value;
        this.dispatchEvent(new Event('input', {bubbles: true}));
        this.dispatchEvent(new Event('change', {bubbles: true}));
        return {ok: this.value === value, value: this.value};
      }`,
      arguments: [{value: String(payload.value || '')}],
      returnByValue: true,
    });
    if (!response.result?.value?.ok) {
      throw browserError('SELECT_FAILED', '目标不是原生下拉框或选项值不存在');
    }
    return this.snapshot(session);
  }

  async press(payload) {
    const session = this.requireSession(payload.session_id);
    const key = String(payload.key || '');
    // Full-automation mode: allow Enter and keys outside the former allowlist.
    // if (key === 'Enter') {
    //   throw browserError('HUMAN_TAKEOVER_REQUIRED', 'Enter 可能提交表单或触发外部操作，请用户手工完成。');
    // }
    // if (!ALLOWED_KEYS.has(key)) {
    //   throw browserError('KEY_NOT_ALLOWED', `按键 ${key} 不在允许列表中`);
    // }
    await this.send(session.tabId, 'Input.dispatchKeyEvent', {type: 'keyDown', key});
    await this.send(session.tabId, 'Input.dispatchKeyEvent', {type: 'keyUp', key});
    await delay(150);
    return this.snapshot(session);
  }

  async scroll(payload) {
    const session = this.requireSession(payload.session_id);
    const objectId = await this.documentObject(session.tabId);
    const scrolled = await this.send(session.tabId, 'Runtime.callFunctionOn', {
      objectId,
      functionDeclaration: scrollPageContainer.toString(),
      arguments: [{value: finiteNumber(payload.x)}, {value: finiteNumber(payload.y)}],
      returnByValue: true,
    });
    if (scrolled.exceptionDetails) {
      throw browserError('SCROLL_FAILED', '无法滚动页面内容，请获取最新快照后重试');
    }
    await delay(150);
    return {...await this.snapshot(session), scroll: scrolled.result?.value};
  }

  async wait(payload) {
    const session = this.requireSession(payload.session_id);
    const timeout = Math.min(Math.max(Number(payload.timeout_ms) || 5000, 100), 30000);
    const text = String(payload.text || '');
    const url = String(payload.url || '');
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      const objectId = await this.documentObject(session.tabId);
      const response = await this.send(session.tabId, 'Runtime.callFunctionOn', {
        objectId,
        functionDeclaration: `function (text, url) {
          const textOK = !text || (document.body?.innerText || '').includes(text);
          const urlOK = !url || location.href.includes(url);
          return {ok: textOK && urlOK, url: location.href};
        }`,
        arguments: [{value: text}, {value: url}],
        returnByValue: true,
      });
      if (response.result?.value?.ok) return this.snapshot(session);
      await delay(250);
    }
    throw browserError('ACTION_TIMEOUT', '等待页面条件超时');
  }

  async screenshot(payload) {
    const session = this.requireSession(payload.session_id);
    await this.prepareVisiblePage(session);
    const [shot, viewport] = await Promise.all([
      this.send(session.tabId, 'Page.captureScreenshot', {
        format: 'jpeg', quality: 75, fromSurface: true, captureBeyondViewport: false,
      }),
      this.viewportSize(session),
    ]);
    const tab = await chrome.tabs.get(session.tabId);
    return {
      session_id: session.id,
      url: tab.url || '',
      title: tab.title || '',
      mime_type: 'image/jpeg',
      data_base64: shot.data,
      viewport,
    };
  }

  async tabs(payload) {
    const session = this.requireSession(payload.session_id);
    const tab = await chrome.tabs.get(session.tabId);
    return {
      session_id: session.id,
      tabs: [{tab_id: tab.id, url: tab.url || '', title: tab.title || '', active: tab.active}],
    };
  }

  async close(payload) {
    const session = this.requireSession(payload.session_id);
    this.sessions.delete(session.id);
    try { await chrome.debugger.detach({tabId: session.tabId}); } catch {}
    try {
      if (session.windowId) await chrome.windows.remove(session.windowId);
      else await chrome.tabs.remove(session.tabId);
    } catch {}
    return {session_id: session.id, closed: true};
  }

  requireSession(sessionId) {
    const session = this.sessions.get(String(sessionId || ''));
    if (!session) throw browserError('SESSION_NOT_FOUND', '受控浏览器会话不存在或扩展已重启');
    this.ensureAttached(session);
    return session;
  }

  ensureAttached(session) {
    if (session.detached) throw browserError('BROWSER_DETACHED', '浏览器调试连接已断开');
  }

  resolveRef(session, ref, expectedRevision) {
    if (expectedRevision && Number(expectedRevision) !== session.revision) {
      throw browserError('STALE_SNAPSHOT', '页面快照已经变化，请重新获取 snapshot');
    }
    const target = session.refs.get(String(ref || ''));
    if (!target) throw browserError('STALE_SNAPSHOT', '元素引用不存在，请重新获取 snapshot');
    return target;
  }

  async elementRect(session, target, options = {}) {
    if (options.scroll !== false) {
      try {
        await this.send(session.tabId, 'DOM.scrollIntoViewIfNeeded', {
          backendNodeId: target.backendNodeId,
        });
      } catch {}
    }

    try {
      const response = await this.send(session.tabId, 'DOM.getContentQuads', {
        backendNodeId: target.backendNodeId,
      });
      const rect = rectFromQuads(response.quads);
      if (rect) return rect;
    } catch {}

    const objectId = await this.resolveObject(session.tabId, target.backendNodeId);
    const response = await this.send(session.tabId, 'Runtime.callFunctionOn', {
      objectId,
      functionDeclaration: `function (shouldScroll) {
        const visibleRect = (rect) => rect && rect.width > 0 && rect.height > 0
          ? {x: rect.x, y: rect.y, width: rect.width, height: rect.height}
          : null;
        const scrollTarget = this && this.nodeType === 1 ? this : this?.parentElement;
        if (shouldScroll) {
          scrollTarget?.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
        }

        if (this && this.nodeType === 3) {
          const range = document.createRange();
          range.selectNodeContents(this);
          const textRect = visibleRect(range.getBoundingClientRect());
          range.detach?.();
          if (textRect) return textRect;
        }

        let candidate = this && this.nodeType === 1 ? this : this?.parentElement;
        for (let depth = 0; candidate && depth < 5; depth += 1, candidate = candidate.parentElement) {
          const rect = visibleRect(candidate.getBoundingClientRect?.());
          if (rect) return rect;
        }
        return null;
      }`,
      arguments: [{value: options.scroll !== false}],
      returnByValue: true,
    });
    const rect = response.result?.value;
    if (!rect || rect.width <= 0 || rect.height <= 0) {
      throw browserError('ELEMENT_NOT_VISIBLE', '目标元素不可见或没有可点击区域');
    }
    return rect;
  }

  async viewportSize(session) {
    const metrics = await this.send(session.tabId, 'Page.getLayoutMetrics');
    const viewport = metrics.cssVisualViewport || metrics.cssLayoutViewport ||
      metrics.visualViewport || metrics.layoutViewport || {};
    const width = Number(viewport.clientWidth || viewport.width);
    const height = Number(viewport.clientHeight || viewport.height);
    if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
      throw browserError('VIEWPORT_UNAVAILABLE', '无法获取浏览器视口尺寸');
    }
    return {width, height};
  }

  async dispatchClick(tabId, x, y) {
    await this.send(tabId, 'Input.dispatchMouseEvent', {type: 'mouseMoved', x, y});
    await this.send(tabId, 'Input.dispatchMouseEvent', {type: 'mousePressed', x, y, button: 'left', clickCount: 1});
    await this.send(tabId, 'Input.dispatchMouseEvent', {type: 'mouseReleased', x, y, button: 'left', clickCount: 1});
  }

  async inspectPoint(session, point) {
    const response = await this.send(session.tabId, 'Runtime.evaluate', {
      expression: `(() => {
        const describe = (element) => {
          if (!element) return null;
          return {
            tag: String(element.tagName || '').toLowerCase(),
            role: element.getAttribute?.('role') || '',
            aria_label: element.getAttribute?.('aria-label') || '',
            contenteditable: element.getAttribute?.('contenteditable') || '',
            editable: Boolean(element.isContentEditable),
            readonly: Boolean(element.readOnly),
          };
        };
        return {
          hit_target: describe(document.elementFromPoint(${JSON.stringify(point.x)}, ${JSON.stringify(point.y)})),
          focused: describe(document.activeElement),
        };
      })()`,
      returnByValue: true,
      silent: true,
    });
    return response.result?.value || {};
  }

  async clearFocusedElement(session) {
    await this.send(session.tabId, 'Runtime.evaluate', {
      expression: `(() => {
        const element = document.activeElement;
        if (!element) return false;
        if ('value' in element) {
          const prototype = Object.getPrototypeOf(element);
          const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set;
          if (setter) setter.call(element, ''); else element.value = '';
          element.dispatchEvent(new Event('input', {bubbles: true}));
          element.dispatchEvent(new Event('change', {bubbles: true}));
          return true;
        }
        if (element.isContentEditable) {
          document.execCommand('selectAll', false, null);
          document.execCommand('delete', false, null);
          return true;
        }
        return false;
      })()`,
      returnByValue: true,
      silent: true,
    });
  }

  async verifyVisibleText(session, rawText) {
    const text = String(rawText || '');
    if (!text) return;
    const deadline = Date.now() + 3000;
    while (Date.now() < deadline) {
      const response = await this.send(session.tabId, 'Runtime.evaluate', {
        expression: `(() => (document.body?.innerText || '').includes(${JSON.stringify(text)}))()`,
        returnByValue: true,
        silent: true,
      });
      if (response.result?.value) return;
      await delay(100);
    }
    throw browserError('TYPE_NOT_APPLIED', `输入完成后页面未出现校验文本“${text.slice(0, 80)}”`);
  }

  async resolveObject(tabId, backendNodeId) {
    const response = await this.send(tabId, 'DOM.resolveNode', {backendNodeId});
    const objectId = response.object?.objectId;
    if (!objectId) throw browserError('STALE_SNAPSHOT', '页面节点已失效，请重新获取 snapshot');
    return objectId;
  }

  async documentObject(tabId) {
    const response = await this.send(tabId, 'Runtime.evaluate', {
      expression: 'document', returnByValue: false, silent: true,
    });
    const objectId = response.result?.objectId;
    if (!objectId) throw browserError('PAGE_UNAVAILABLE', '无法访问当前文档');
    return objectId;
  }

  send(tabId, method, params = {}) {
    return chrome.debugger.sendCommand({tabId}, method, params);
  }

  handleDetach(source) {
    for (const session of this.sessions.values()) {
      if (session.tabId === source.tabId) session.detached = true;
    }
  }
}

// Runs inside the page through CDP; keep this function self-contained.
export function scrollPageContainer(x, y) {
  const root = document.scrollingElement || document.documentElement;
  const horizontal = Math.abs(x) > Math.abs(y);
  const candidates = [root, ...document.querySelectorAll('*')].filter((el, index, all) => {
    if (!el || all.indexOf(el) !== index) return false;
    const rect = el.getBoundingClientRect();
    const style = getComputedStyle(el);
    const overflow = horizontal ? style.overflowX : style.overflowY;
    const range = horizontal ? el.scrollWidth - el.clientWidth : el.scrollHeight - el.clientHeight;
    return range > 1 && (el === root || /auto|scroll|overlay/.test(overflow))
      && style.visibility !== 'hidden' && style.display !== 'none'
      && rect.bottom > 0 && rect.right > 0 && rect.top < innerHeight && rect.left < innerWidth;
  });
  const area = (el) => {
    const r = el.getBoundingClientRect();
    return Math.max(0, Math.min(r.right, innerWidth) - Math.max(r.left, 0))
      * Math.max(0, Math.min(r.bottom, innerHeight) - Math.max(r.top, 0));
  };
  // Prefer the main visible document pane over narrow sidebars. Keep the same
  // pane at its boundary, so reaching the end does not scroll an unrelated UI.
  candidates.sort((a, b) => area(b) - area(a));
  const target = candidates[0] || root;
  const before = {x: target.scrollLeft, y: target.scrollTop};
  target.scrollBy({left: x, top: y, behavior: 'instant'});
  const after = {x: target.scrollLeft, y: target.scrollTop};
  return {
    target: target === root ? 'document' : 'container',
    before, after, moved: before.x !== after.x || before.y !== after.y,
    at_boundary: horizontal
      ? (x >= 0 ? target.scrollLeft + target.clientWidth >= target.scrollWidth - 1 : target.scrollLeft <= 0)
      : (y >= 0 ? target.scrollTop + target.clientHeight >= target.scrollHeight - 1 : target.scrollTop <= 0),
  };
}

function validateTargetURL(raw, allowPrivateNetwork) {
  let parsed;
  try { parsed = new URL(String(raw || '')); } catch {
    throw browserError('INVALID_URL', 'URL 格式无效');
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw browserError('URL_NOT_ALLOWED', '只允许打开 http/https URL');
  }
  const host = parsed.hostname.toLowerCase();
  if (!allowPrivateNetwork && isPrivateHost(host)) {
    throw browserError('PRIVATE_NETWORK_BLOCKED', '本地或内网地址需要用户明确允许');
  }
  parsed.username = '';
  parsed.password = '';
  return parsed.toString();
}

function isPrivateHost(host) {
  const normalized = String(host || '').toLowerCase().replace(/^\[|\]$/g, '').replace(/\.$/, '');
  if (!normalized || normalized === 'localhost' || normalized.endsWith('.localhost')
      || normalized.endsWith('.local')) return true;
  if (normalized === 'metadata.google.internal') return true;

  const octets = normalized.split('.').map(Number);
  if (octets.length === 4 && octets.every((part) => Number.isInteger(part) && part >= 0 && part <= 255)) {
    const [first, second] = octets;
    if (first === 0 || first === 10 || first === 127 || first >= 224) return true;
    if (first === 100 && second >= 64 && second <= 127) return true;
    if (first === 169 && second === 254) return true;
    if (first === 172 && second >= 16 && second <= 31) return true;
    if (first === 192 && second === 168) return true;
  }

  if (normalized === '::' || normalized === '::1') return true;
  if (/^f[cd][0-9a-f]{2}:/.test(normalized) || /^fe[89ab][0-9a-f]:/.test(normalized)) return true;
  if (normalized.startsWith('::ffff:')) return isPrivateHost(normalized.slice('::ffff:'.length));
  return false;
}

function finiteNumber(value) {
  const number = Number(value);
  return Number.isFinite(number) ? number : 0;
}

export function normalizeViewportPoint(rawX, rawY, viewport) {
  const x = Number(rawX);
  const y = Number(rawY);
  const width = Number(viewport?.width);
  const height = Number(viewport?.height);
  if (![x, y, width, height].every(Number.isFinite) || width <= 0 || height <= 0) {
    throw browserError('INVALID_COORDINATES', '坐标或视口尺寸无效');
  }
  if (x < 0 || y < 0 || x >= width || y >= height) {
    throw browserError(
      'COORDINATE_OUT_OF_BOUNDS',
      `坐标 (${x}, ${y}) 超出视口 ${width}x${height}`,
    );
  }
  return {x, y};
}

export function intersectionPoint(rowRect, columnRect, viewport) {
  const rowY = Number(rowRect?.y) + Number(rowRect?.height) / 2;
  const columnX = Number(columnRect?.x) + Number(columnRect?.width) / 2;
  if (![rowY, columnX].every(Number.isFinite)) {
    throw browserError('INVALID_INTERSECTION', '无法从行标签和列标题计算交点');
  }
  return normalizeViewportPoint(columnX, rowY, viewport);
}

export function rectFromQuads(quads) {
  for (const quad of quads || []) {
    if (!Array.isArray(quad) || quad.length < 8) continue;
    const xs = [Number(quad[0]), Number(quad[2]), Number(quad[4]), Number(quad[6])];
    const ys = [Number(quad[1]), Number(quad[3]), Number(quad[5]), Number(quad[7])];
    if (![...xs, ...ys].every(Number.isFinite)) continue;
    const x = Math.min(...xs);
    const y = Math.min(...ys);
    const width = Math.max(...xs) - x;
    const height = Math.max(...ys) - y;
    if (width > 0 && height > 0) return {x, y, width, height};
  }
  return null;
}

const INVISIBLE_ONLY_TEXT_PATTERN = /[\s\u200B-\u200F\u202A-\u202E\u2060-\u206F\uFEFF]/g;
const SAFE_REMOVABLE_INVISIBLE_TEXT_PATTERN = /[\u200B\u2060\uFEFF]/g;

export function normalizeText(value) {
  const text = String(value || '');
  if (!text || text.replace(INVISIBLE_ONLY_TEXT_PATTERN, '') === '') return '';
  return text
    .replace(SAFE_REMOVABLE_INVISIBLE_TEXT_PATTERN, '')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, 500);
}

function isInvisibleOnlyText(value) {
  const text = String(value || '');
  return Boolean(text) && text.replace(INVISIBLE_ONLY_TEXT_PATTERN, '') === '';
}

function waitForTabComplete(tabId, timeout) {
  return new Promise(async (resolve) => {
    try {
      const current = await chrome.tabs.get(tabId);
      if (current.status === 'complete') {
        resolve();
        return;
      }
    } catch {
      resolve();
      return;
    }
    const timer = setTimeout(() => {
      chrome.tabs.onUpdated.removeListener(listener);
      resolve();
    }, timeout);
    const listener = (updatedTabId, changeInfo) => {
      if (updatedTabId !== tabId || changeInfo.status !== 'complete') return;
      clearTimeout(timer);
      chrome.tabs.onUpdated.removeListener(listener);
      resolve();
    };
    chrome.tabs.onUpdated.addListener(listener);
  });
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function browserError(code, message, details) {
  const error = new Error(message);
  error.code = code;
  error.details = details;
  return error;
}

function normalizeError(error, fallbackCode) {
  if (error?.code) return error;
  return browserError(fallbackCode, String(error?.message || error));
}
