'use strict';

(() => {
  const shell = window.parent;
  const SETTLE_MS = 500;
  const INTERCEPTED = ['pointerdown', 'pointerup', 'mousedown', 'mouseup', 'click', 'dblclick', 'auxclick', 'contextmenu',
    'touchstart', 'touchend', 'keydown', 'keypress', 'keyup', 'beforeinput', 'input', 'change', 'submit', 'focusin', 'focus'];
  const tabIndexes = new Map();
  let picking = false;
  let settleUntil = 0;
  let reported = '';

  function post(kind, data) {
    shell.postMessage(Object.assign({ lavagna: kind }, data), '*');
  }

  function anchors() {
    return document.querySelectorAll('[data-ref]');
  }

  function setPicking(active) {
    if (active === picking) return;
    if (!active) settleUntil = performance.now() + SETTLE_MS;
    picking = active;
    document.body.classList.toggle('picking', active);
    for (const el of anchors()) {
      if (active && !tabIndexes.has(el)) {
        tabIndexes.set(el, el.getAttribute('tabindex'));
        el.tabIndex = 0;
      }
    }
    if (!active) {
      for (const [el, previous] of tabIndexes) {
        if (previous === null) el.removeAttribute('tabindex');
        else el.setAttribute('tabindex', previous);
      }
      tabIndexes.clear();
      return;
    }
    const first = document.querySelector('[data-ref]');
    if (first) first.focus({ preventScroll: true });
  }

  function mark(ref) {
    for (const el of document.querySelectorAll('.referenced')) el.classList.remove('referenced');
    for (const el of anchors()) {
      if (el.dataset.ref === ref) el.classList.add('referenced');
    }
  }

  function intercept(event) {
    if (!picking && performance.now() >= settleUntil) return;
    event.stopImmediatePropagation();
    if (!picking) {
      if (event.cancelable) event.preventDefault();
      return;
    }
    if (event.type === 'keydown' && event.key === 'Tab') return;
    if (event.cancelable) event.preventDefault();
    const target = event.target instanceof Element ? event.target.closest('[data-ref]') : null;
    if (event.type === 'click' && target) post('anchor', { ref: target.dataset.ref });
    if (event.type !== 'keydown') return;
    if (event.key === 'Escape') post('cancel', {});
    else if ((event.key === 'Enter' || event.key === ' ') && target) post('anchor', { ref: target.dataset.ref });
  }

  function layout() {
    const chapters = {};
    for (const section of document.querySelectorAll('main > section.chapter')) {
      chapters[section.id] = Math.round(section.getBoundingClientRect().top + window.scrollY);
    }
    const height = Math.ceil(document.body.scrollHeight);
    const report = JSON.stringify({ height, chapters });
    if (report === reported) return;
    reported = report;
    post('layout', { height, chapters });
  }

  for (const type of INTERCEPTED) window.addEventListener(type, intercept, true);

  window.addEventListener('message', event => {
    const data = event.data;
    if (event.source !== shell || !data || typeof data !== 'object') return;
    if (data.lavagna === 'pick') setPicking(data.active === true);
    if (data.lavagna === 'mark') mark(typeof data.ref === 'string' ? data.ref : null);
  });

  document.addEventListener('DOMContentLoaded', () => {
    new ResizeObserver(layout).observe(document.body);
    layout();
  });
  window.addEventListener('load', layout);
})();
