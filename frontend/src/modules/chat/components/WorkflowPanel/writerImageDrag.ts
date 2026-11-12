export interface WriterImageDropTarget {
  element: HTMLElement;
  position: 'before' | 'after' | 'child';
}

export function startWriterImageDrag(
  event: MouseEvent,
  image: HTMLImageElement,
  root: HTMLElement,
  resolveTarget: (x: number, y: number) => WriterImageDropTarget | null,
  onDrop: (target: WriterImageDropTarget) => void,
): () => void {
  let x = event.clientX, y = event.clientY;
  let preview: HTMLImageElement | undefined;
  let indicator: HTMLDivElement | undefined;
  let frame = 0;
  let offsetX = 0, offsetY = 0;
  let finished = false;

  const preventNativeDrag = (nativeEvent: DragEvent) => nativeEvent.preventDefault();
  const suppressClick = (click: MouseEvent) => { click.preventDefault(); click.stopPropagation(); };
  const cancel = () => {
    if (finished) return;
    finished = true;
    window.cancelAnimationFrame(frame);
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', finish, true);
    window.removeEventListener('keydown', keyDown, true);
    window.removeEventListener('blur', cancel);
    window.removeEventListener('dragstart', preventNativeDrag, true);
    window.setTimeout(() => window.removeEventListener('click', suppressClick, true), 0);
    image.classList.remove('writer-image-drag-source');
    document.documentElement.classList.remove('writer-image-dragging');
    preview?.remove(); indicator?.remove();
  };

  const update = () => {
    if (!image.isConnected || !root.isConnected) { cancel(); return; }
    preview!.style.transform = `translate3d(${x - offsetX}px, ${y - offsetY}px, 0)`;
    const hovered = document.elementFromPoint(x, y);
    if (hovered && root.contains(hovered)) {
      let scrolledX = false, scrolledY = false;
      for (let element: Element | null = hovered; element; element = element.parentElement) {
        if (!(element instanceof HTMLElement)) continue;
        const rect = element.getBoundingClientRect(), style = window.getComputedStyle(element);
        const speed = (point: number, start: number, end: number) =>
          point < start + 36 ? -12 * Math.min(1, (start + 36 - point) / 36)
            : point > end - 36 ? 12 * Math.min(1, (point - end + 36) / 36) : 0;
        if (!scrolledX && /auto|scroll/.test(style.overflowX) && element.scrollWidth > element.clientWidth) {
          const previous = element.scrollLeft;
          element.scrollLeft += speed(x, rect.left, rect.right);
          scrolledX = element.scrollLeft !== previous;
        }
        if (!scrolledY && /auto|scroll/.test(style.overflowY) && element.scrollHeight > element.clientHeight) {
          const previous = element.scrollTop;
          element.scrollTop += speed(y, rect.top, rect.bottom);
          scrolledY = element.scrollTop !== previous;
        }
      }
    }
    const target = resolveTarget(x, y);
    indicator!.hidden = !target;
    if (target) {
      const rect = target.element.getBoundingClientRect();
      const horizontal = Boolean(target.element.closest('.writer-image-group[data-layout="sliding"]'));
      const child = target.position === 'child', after = target.position === 'after';
      indicator!.dataset.position = target.position;
      Object.assign(indicator!.style, {
        left: `${horizontal && after && !child ? rect.right : rect.left}px`,
        top: `${!horizontal && after && !child ? rect.bottom : rect.top}px`,
        width: `${child || !horizontal ? rect.width : 3}px`,
        height: `${child || horizontal ? rect.height : 3}px`,
      });
    }
    frame = window.requestAnimationFrame(update);
  };

  const move = (moveEvent: MouseEvent) => {
    x = moveEvent.clientX; y = moveEvent.clientY;
    if (!preview) {
      if (Math.hypot(x - event.clientX, y - event.clientY) < 5) return;
      const rect = image.getBoundingClientRect();
      const scale = Math.min(1, 320 / Math.max(rect.width, 1), 240 / Math.max(rect.height, 1));
      offsetX = Math.max(0, Math.min(rect.width, event.clientX - rect.left)) * scale;
      offsetY = Math.max(0, Math.min(rect.height, event.clientY - rect.top)) * scale;
      preview = image.cloneNode(false) as HTMLImageElement;
      preview.removeAttribute('id');
      preview.className = 'writer-image-drag-preview';
      preview.setAttribute('aria-hidden', 'true');
      Object.assign(preview.style, { width: `${rect.width * scale}px`, height: `${rect.height * scale}px` });
      indicator = document.createElement('div');
      indicator.className = 'writer-image-drag-indicator';
      indicator.setAttribute('aria-hidden', 'true');
      document.body.append(preview, indicator);
      image.classList.add('writer-image-drag-source');
      document.documentElement.classList.add('writer-image-dragging');
      window.addEventListener('click', suppressClick, true);
      update();
    }
    moveEvent.preventDefault();
  };
  const finish = (up: MouseEvent) => {
    const target = preview && image.isConnected && root.isConnected ? resolveTarget(up.clientX, up.clientY) : null;
    if (preview) { up.preventDefault(); up.stopPropagation(); }
    cancel();
    if (target) onDrop(target);
  };
  const keyDown = (key: KeyboardEvent) => {
    if (key.key !== 'Escape') return;
    key.preventDefault(); key.stopPropagation(); cancel();
  };
  window.addEventListener('mousemove', move);
  window.addEventListener('mouseup', finish, true);
  window.addEventListener('keydown', keyDown, true);
  window.addEventListener('blur', cancel);
  window.addEventListener('dragstart', preventNativeDrag, true);
  return cancel;
}
