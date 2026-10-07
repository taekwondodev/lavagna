'use strict';

// The content frame helper: it reports the document's layout to the shell and
// follows the option the shell reports, so 02 shows the variant in use. The
// shell sends only the selected option id and whether a free-text answer
// exists; the preview chosen here never leaves the frame.
(() => {
  const shell = window.parent;
  const root = document.documentElement;
  let option = null;
  let free = false;
  let preview = null;
  let reported = '';
  let announced = '';

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
    if (event.source !== shell || !data || typeof data !== 'object' || data.lavagna !== 'option') return;
    const next = typeof data.option === 'string' ? data.option : null;
    const nextFree = data.free === true && !next;
    if (next !== option || nextFree !== free) preview = null;
    option = next;
    free = nextFree;
    apply();
  });

  document.addEventListener('click', event => {
    const chip = event.target instanceof Element ? event.target.closest('.preview .chip') : null;
    if (!chip) return;
    preview = preview === chip.dataset.variant ? null : chip.dataset.variant;
    apply();
  });

  document.addEventListener('DOMContentLoaded', () => {
    apply();
    new ResizeObserver(layout).observe(document.body);
    layout();
  });
  window.addEventListener('load', layout);
})();
