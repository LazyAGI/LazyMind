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
  let lastFrame: number | undefined;
  const scrollParents: HTMLElement[] = [];
  for (let element = image.parentElement; element; element = element.parentElement) {
    const style = window.getComputedStyle(element);
    if (/auto|scroll/.test(`${style.overflowX} ${style.overflowY}`)) scrollParents.push(element);
  }

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

  const update = (time?: number) => {
    if (!image.isConnected || !root.isConnected) { cancel(); return; }
    const elapsed = time !== undefined && lastFrame !== undefined ? Math.min(32, Math.max(0, time - lastFrame)) : 16;
    lastFrame = time;
    preview!.style.transform = `translate3d(${x - offsetX}px, ${y - offsetY}px, 0)`;
    let scrolledX = false, scrolledY = false;
    for (let index = 0; index < scrollParents.length; index++) {
      const element = scrollParents[index], style = window.getComputedStyle(element);
      const rects = scrollParents.slice(index).map(parent => parent.getBoundingClientRect());
      const left = Math.max(0, ...rects.map(rect => rect.left));
      const right = Math.min(window.innerWidth, ...rects.map(rect => rect.right));
      const top = Math.max(0, ...rects.map(rect => rect.top));
      const bottom = Math.min(window.innerHeight, ...rects.map(rect => rect.bottom));
      if (right <= left || bottom <= top) continue;
      const speed = (point: number, start: number, end: number, range: number, maxSpeed: number) => {
        const edge = Math.min(range, (end - start) / 2);
        return point < start + edge ? -maxSpeed * elapsed * Math.min(1, (start + edge - point) / edge)
          : point > end - edge ? maxSpeed * elapsed * Math.min(1, (point - end + edge) / edge) : 0;
      };
      if (!scrolledX && y >= top - 64 && y <= bottom + 64 && /auto|scroll/.test(style.overflowX) && element.scrollWidth > element.clientWidth) {
        const previous = element.scrollLeft;
        element.scrollLeft += speed(x, left, right, 64, .72);
        scrolledX = element.scrollLeft !== previous;
      }
      if (!scrolledY && x >= left - 64 && x <= right + 64 && /auto|scroll/.test(style.overflowY) && element.scrollHeight > element.clientHeight) {
        const previous = element.scrollTop;
        element.scrollTop += speed(y, top, bottom, 96, 1.2);
        scrolledY = element.scrollTop !== previous;
      }
    }
    const target = resolveTarget(x, y);
    indicator!.hidden = !target;
    if (target) {
      const rect = target.element.getBoundingClientRect();
      const horizontal = Boolean(target.element.parentElement?.closest('.writer-image-group[data-layout="sliding"]'));
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
