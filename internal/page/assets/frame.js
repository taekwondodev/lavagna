'use strict';

// The content frame helper: it reports the document's layout to the shell and
// follows the option the shell reports, so 02 shows the variant in use. The
// shell sends only the selected option id and whether a free-text answer
// exists; the preview chosen here never leaves the frame. It also fits raw
// HTML blocks wider than the column and asks the shell to expand one.
(() => {
  const EXPAND_BELOW = 0.85;
  const shell = window.parent;
  const root = document.documentElement;
  let option = null;
  let free = false;
  let preview = null;
  let reported = '';
  let announced = '';
  // expanded and native are the shell's last `expanded` state: native shows
  // the expanding block full screen at its own size, else it stays fitted.
  let expanded = false;
  let native = false;
  let expanding = null;

  function post(kind, data) {
    shell.postMessage(Object.assign({ lavagna: kind }, data), '*');
  }

  function recommended() {
    const chip = document.querySelector('.preview [data-recommended]');
    return chip ? chip.dataset.variant : null;
  }

  // The preview, else the choice, else the recommended option. A free-text
  // answer shows the recommended variant until the agent draws it.
  function variant() {
    return preview || option || recommended();
  }

  function note() {
    if (preview) return 'anteprima: non è una scelta';
    if (free) return 'risposta libera: l’agente la disegna nel prossimo turno';
    if (option) return 'segue la tua scelta';
    return recommended() ? 'mostra la consigliata' : '';
  }

  function apply() {
    const shown = variant();
    if (option) root.dataset.option = option; else delete root.dataset.option;
    root.dataset.free = String(free);
    if (shown) root.dataset.variant = shown; else delete root.dataset.variant;
    for (const chip of document.querySelectorAll('.preview .chip')) {
      const on = chip.dataset.variant === shown;
      chip.classList.toggle('on', on);
      chip.setAttribute('aria-pressed', String(on));
    }
    for (const effect of document.querySelectorAll('.effect')) effect.hidden = effect.dataset.variant !== shown;
    const label = document.querySelector('.preview-note');
    if (label) label.textContent = note();
    const state = JSON.stringify([option, free, shown]);
    if (state === announced) return;
    announced = state;
    document.dispatchEvent(new CustomEvent('lavagna:option', { detail: { option, free, variant: shown } }));
    fit();
  }

  function head(block) {
    let node = block.querySelector(':scope > .raw-head');
    if (node) return node;
    node = document.createElement('div');
    node.className = 'raw-head';
    const label = document.createElement('span');
    label.className = 'raw-scale';
    const control = document.createElement('button');
    control.type = 'button';
    control.className = 'raw-expand';
    node.append(label, control);
    block.prepend(node);
    return node;
  }

  // fitBlock measures the block at the column width and, when it overflows,
  // zooms its stage to fit. The expanding block keeps its header so Riduci
  // stays reachable, and at native size it scrolls instead of zooming.
  function fitBlock(block) {
    const stage = block.querySelector(':scope > .raw-stage');
    const open = expanded && block === expanding;
    block.classList.remove('wide');
    block.classList.toggle('expanded', open);
    stage.style.width = '';
    stage.style.zoom = '';
    const width = stage.scrollWidth;
    if (width <= stage.clientWidth && !open) return;
    block.classList.add('wide');
    const node = head(block);
    const scale = open && native ? 1 : Math.min(1, stage.clientWidth / width);
    if (!(open && native)) {
      stage.style.width = width + 'px';
      stage.style.zoom = String(scale);
    }
    node.firstChild.textContent = 'Schermata ' + width + ' px · ' + Math.round(scale * 100) + ' %';
    const control = node.lastChild;
    control.textContent = open ? 'Riduci' : 'Espandi a tutta pagina';
    control.hidden = !open && scale >= EXPAND_BELOW;
  }

  function fit() {
    for (const block of document.querySelectorAll('.raw')) fitBlock(block);
  }

  // follow applies the shell's `expanded` state. Without a block asking, as
  // after a forged `expand`, the first wide block is shown, else none.
  function follow(data) {
    expanded = data.active === true;
    native = expanded && data.native === true;
    if (!expanded) expanding = null;
    else if (!expanding) expanding = document.querySelector('.raw.wide');
    if (expanded && !expanding) {
      expanded = native = false;
      post('expand', { active: false });
    }
    if (native) root.dataset.expanded = 'native'; else delete root.dataset.expanded;
    fit();
  }

  function layout() {
    const sections = {};
    for (const section of document.querySelectorAll('main > section.chapter')) {
      sections[section.id] = Math.round(section.getBoundingClientRect().top + window.scrollY);
    }
    const height = Math.ceil(document.body.scrollHeight);
    const report = JSON.stringify({ height, sections });
    if (report === reported) return;
    reported = report;
    post('layout', { height, sections });
  }

  window.addEventListener('message', event => {
    const data = event.data;
    if (event.source !== shell || !data || typeof data !== 'object') return;
    if (data.lavagna === 'expanded') follow(data);
    if (data.lavagna !== 'option') return;
    const next = typeof data.option === 'string' ? data.option : null;
    const nextFree = data.free === true && !next;
    if (next !== option || nextFree !== free) preview = null;
    option = next;
    free = nextFree;
    apply();
  });

  document.addEventListener('click', event => {
    const target = event.target instanceof Element ? event.target : null;
    const control = target && target.closest('.raw-expand');
    if (control) {
      const block = control.closest('.raw');
      const open = expanded && block === expanding;
      if (!open) expanding = block;
      post('expand', { active: !open });
      return;
    }
    const chip = target && target.closest('.preview .chip');
    if (!chip) return;
    preview = preview === chip.dataset.variant ? null : chip.dataset.variant;
    apply();
  });

  document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && expanded) post('expand', { active: false });
  });

  document.addEventListener('DOMContentLoaded', () => {
    apply();
    new ResizeObserver(layout).observe(document.body);
    layout();
  });
  window.addEventListener('resize', fit);
  window.addEventListener('load', () => {
    fit();
    layout();
  });
})();
