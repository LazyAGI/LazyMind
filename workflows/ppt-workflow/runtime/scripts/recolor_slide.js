// Deterministic palette edit. The source HTML, chart data, images and geometry
// remain intact; this runs when the preview/export document is rendered.
(() => {
  const light = document.currentScript.dataset.theme === 'light';
  const palette = light
    ? { background: '#FFFFFF', panel: '#F1F5F9', text: '#111111', muted: '#475569', border: '#CBD5E1' }
    : { background: '#0B1220', panel: '#152238', text: '#F1F5F9', muted: '#B8C5D6', border: '#475569' };
  const rgb = value => {
    const match = String(value).match(/^rgba?\(([^)]+)\)$/);
    if (!match) return null;
    const values = match[1].split(/[\s,/]+/).filter(Boolean).map(Number);
    return values.length >= 3 ? [...values.slice(0, 3), values[3] ?? 1] : null;
  };
  const withAlpha = (target, source) => {
    const c = rgb(source);
    if (!c || c[3] >= 1) return target;
    const channels = [1, 3, 5].map(i => parseInt(target.slice(i, i + 2), 16));
    return `rgba(${channels.join(',')},${c[3]})`;
  };
  const neutral = value => {
    const c = rgb(value);
    return c && c[3] > 0 && (Math.max(...c.slice(0, 3)) - Math.min(...c.slice(0, 3)) < 90 || Math.max(...c.slice(0, 3)) < 100);
  };
  const apply = () => {
    const nodes = [...document.querySelectorAll('html, body, body *')].filter(el => {
      const chartRoot = el.closest('[id^="chart_"]');
      return !(chartRoot && window.echarts?.getInstanceByDom(chartRoot)) &&
        !el.closest('script, style, noscript, img, video, canvas') &&
        !['SCRIPT', 'STYLE', 'IMG', 'VIDEO', 'CANVAS'].includes(el.tagName);
    });
    // Snapshot first: changing ancestors must not alter inherited source styles.
    const snapshots = nodes.map(el => {
      const s = getComputedStyle(el);
      return {el, color: s.color, bg: s.backgroundColor, image: s.backgroundImage,
        fill: s.fill, stroke: s.stroke, border: s.borderTopColor, shadow: s.boxShadow};
    });
    for (const {el, bg, image, fill, stroke, border, shadow} of snapshots) {
      const root = el.matches('html,body,.wrapper,[data-slide-root],.slide');
      el.style.setProperty('color', palette.text, 'important');
      if (root || neutral(bg)) el.style.setProperty('background-color', root ? palette.background : palette.panel, 'important');
      // Pure CSS gradients are theme decoration. Never replace url() assets.
      if (image !== 'none' && !image.includes('url(')) el.style.setProperty('background-image', 'none', 'important');
      if (neutral(border)) el.style.setProperty('border-color', withAlpha(palette.border, border), 'important');
      if (shadow !== 'none') el.style.setProperty('box-shadow', 'none', 'important');
      if (el.namespaceURI === 'http://www.w3.org/2000/svg') {
        if (el.tagName.toLowerCase() === 'text' || el.closest('text')) {
          el.style.setProperty('fill', palette.text, 'important');
        } else if (!el.closest('[id^="chart_"]')) {
          if (neutral(fill)) el.style.setProperty('fill', withAlpha(palette.panel, fill), 'important');
          if (neutral(stroke)) el.style.setProperty('stroke', withAlpha(palette.muted, stroke), 'important');
        }
      }
    }
    for (const el of document.querySelectorAll('[id^="chart_"]')) {
      const chart = window.echarts?.getInstanceByDom(el);
      if (!chart) continue;
      const option = chart.getOption();
      const patch = { animation: false, backgroundColor: 'transparent', textStyle: {color: palette.text} };
      for (const key of ['xAxis', 'yAxis', 'radiusAxis', 'angleAxis']) {
        if (option[key]) patch[key] = option[key].map(axis => ({
          id: axis.id, axisLabel: {color: palette.muted}, nameTextStyle: {color: palette.muted},
          axisLine: {lineStyle: {color: palette.border}}, splitLine: {lineStyle: {color: palette.border}},
        }));
      }
      if (option.legend) patch.legend = option.legend.map(legend => ({id: legend.id, textStyle: {color: palette.text}}));
      if (option.title) patch.title = option.title.map(title => ({id: title.id, textStyle: {color: palette.text}, subtextStyle: {color: palette.muted}}));
      if (option.series) patch.series = option.series.map(series => ({id: series.id, type: series.type, label: {color: palette.text}}));
      if (option.graphic) {
        patch.graphic = JSON.parse(JSON.stringify(option.graphic));
        const visit = item => {
          if (item.type === 'text' && item.style) item.style.fill = palette.text;
          for (const child of [...(item.elements || []), ...(item.children || [])]) visit(child);
        };
        for (const item of patch.graphic) visit(item);
      }
      chart.setOption(patch);
    }
    document.documentElement.dataset.lazymindThemeReady = light ? 'light' : 'dark';
  };
  // Existing inline chart initialization runs before this appended script.
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', apply, {once: true});
  else apply();
  // Async chart initialization may finish at window load; patch those options too.
  window.addEventListener('load', apply, {once: true});
})();
