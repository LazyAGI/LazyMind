/**
 * Make SenseNova-style slide HTML work inside iframe srcDoc.
 * Generated pages reference ../assets/echarts.min.js which cannot resolve in srcDoc.
 * Rewrite that src to the Vite-served absolute URL of the frontend echarts bundle.
 */

// Vite resolves this to a served URL in both dev and production builds.
import echartsMinUrl from 'echarts/dist/echarts.min.js?url';

const ECHARTS_SRC_RE =
  /<script\b[^>]*\bsrc=["'][^"']*echarts[^"']*\.js["'][^>]*>\s*<\/script>/gi;

function absoluteEchartsSrc(): string {
  try {
    return new URL(echartsMinUrl, window.location.origin).href;
  } catch {
    return echartsMinUrl;
  }
}

function rewriteTag(): string {
  const src = absoluteEchartsSrc();
  return `<script data-lazymind-echarts="1" src="${src}"></script>`;
}

/** True when the page expects ECharts (script tag or chart_* container). */
export function htmlNeedsEcharts(html: string): boolean {
  if (!html) return false;
  if (/data-lazymind-echarts=/i.test(html)) return false;
  if (ECHARTS_SRC_RE.test(html)) {
    ECHARTS_SRC_RE.lastIndex = 0;
    return true;
  }
  return /\bid=["']chart_\d+["']/i.test(html) || /\becharts\.init\s*\(/i.test(html);
}

/**
 * Point echarts <script src> at the app-hosted bundle (absolute URL) so srcDoc
 * iframes can load it. Also forces animation:false via a tiny prelude.
 */
export async function htmlWithInlinedEcharts(html: string): Promise<string> {
  if (!html || /data-lazymind-echarts=/i.test(html)) return html;
  if (!htmlNeedsEcharts(html)) return html;

  const tag = rewriteTag();
  // Disable chart tween after echarts loads; does not intercept UMD assignment.
  const prelude =
    '<script data-lazymind-echarts-prelude="1">' +
    '(function(){' +
    'function freeze(){' +
    'var e=window.echarts;if(!e||!e.init||e.__lmStatic)return;' +
    'e.__lmStatic=1;' +
    'var init=e.init.bind(e);' +
    'e.init=function(){' +
    'var c=init.apply(this,arguments);' +
    'if(c&&c.setOption){' +
    'var set=c.setOption.bind(c);' +
    'c.setOption=function(opt,opts){' +
    'if(opt&&typeof opt==="object"){' +
    'opt=Object.assign({},opt,{animation:false,animationDuration:0,animationDurationUpdate:0});' +
    '}' +
    'return set(opt,opts);' +
    '};' +
    '}' +
    'return c;' +
    '};' +
    '}' +
    'var n=0,t=setInterval(function(){freeze();if(window.echarts&&window.echarts.init||++n>80)clearInterval(t);},30);' +
    '})();' +
    '</script>';

  const block = `${prelude}${tag}`;

  if (ECHARTS_SRC_RE.test(html)) {
    ECHARTS_SRC_RE.lastIndex = 0;
    return html.replace(ECHARTS_SRC_RE, block);
  }

  if (/<\/body>/i.test(html)) {
    return html.replace(/<\/body>/i, `${block}</body>`);
  }
  return `${html}\n${block}`;
}

/** @deprecated name kept for callers — now rewrites src, does not inline the bundle. */
export async function getEchartsMinSource(): Promise<string> {
  const resp = await fetch(absoluteEchartsSrc());
  if (!resp.ok) throw new Error(`failed to load echarts bundle: HTTP ${resp.status}`);
  return resp.text();
}
