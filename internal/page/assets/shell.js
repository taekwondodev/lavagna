'use strict';

const TEXT = {
  accepted: 'Ricevuto da lavagna · non ancora consegnato all’agente',
  returned: 'Consegnato al terminale',
  detached: 'Questa scheda non è più collegata alla conversazione',
  uncertain: 'Consegna non riuscita: l’agente è stato interrotto. Il tuo invio è conservato, puoi reinviarlo.',
  carried: 'Bozza ripresa dal round precedente: rivedila prima di inviare.',
};
const DRAFT_LEAD = 'Domande, dubbi e richieste: tutto in un solo invio.';
const SENT_LEAD = 'La pagina si aggiorna da sola al prossimo round.';
const PANELS = {
  draft: { eyebrow: 'Qui agisci tu · bozza non inviata', lead: DRAFT_LEAD },
  sent: { eyebrow: 'Feedback inviato', lead: SENT_LEAD },
  detached: { eyebrow: 'Bozza conservata · non inviata', lead: 'Copia il testo se ti serve: questa scheda non può più inviarlo.' },
};
const PHASES = {
  connecting: { turn: 'Collegamento a lavagna…', panel: 'draft' },
  user: { turn: 'Tocca a te', panel: 'draft' },
  reconnecting: { turn: 'Riconnessione a lavagna…', panel: 'draft' },
  sending: { turn: 'Invio in corso…', panel: 'draft' },
  inactive: { turn: 'Round non più attivo', panel: 'draft' },
  uncertain: { turn: 'Consegna non riuscita', panel: 'draft' },
  unconfirmed: { turn: 'Riconnessione a lavagna…', panel: 'sent' },
  accepted: { turn: 'Inviato', panel: 'sent', delivery: TEXT.accepted, lead: 'In consegna all’agente.' },
  returned: { turn: 'Inviato · consegnato al terminale', panel: 'sent', delivery: TEXT.returned },
  waiting: { turn: 'In attesa del prossimo round', panel: 'sent', delivery: TEXT.returned },
  detached: { turn: 'Scheda non collegata', panel: 'detached', delivery: TEXT.detached },
  closed: { turn: 'Concluso', panel: 'draft' },
};
const STAGES = ['', 'accepted', 'returned'];
const RECORD_KEY = 'lavagna:' + location.pathname;
const CACHE_NAME = 'lavagna:' + location.pathname;
const RETRY_MS = 250;
const COUNTER_FROM = 0.75;
const MAX_FRAME_HEIGHT = 100000;
const LINE_SEPARATORS = new RegExp('[' + String.fromCharCode(0x2028, 0x2029) + ']', 'g');

const $ = selector => document.querySelector(selector);
const form = $('#feedback-form');
const editor = $('#comment-text');
const encoder = new TextEncoder();
const narrow = window.matchMedia('(max-width: 850px)');

let view = null;
let draft = null;
let sending = false;
let live = false;
let detached = false;
let closed = false;
let inactive = false;
let feedbackVisible = false;
let refusal = '';
let picking = false;
let chapterTops = {};
let snapshotURL = null;
let acceptedWhileLive = null;
let leaving = false;

function blank() {
  return { choices: {}, comments: [], editor: '', anchor: null, editing: null, previous: null, next: 0, submission: null, stage: '', sent: null, uncertain: false, carried: false };
}

function stored() {
  try {
    const record = JSON.parse(localStorage.getItem(RECORD_KEY));
    if (record && record.view && record.draft) return record;
  } catch { }
  return null;
}

function roundNumber(round) { return Number(round.replace(/^r/, '')); }

function withComments(target, comments) {
  target.comments = comments.map((comment, index) => ({ id: index + 1, ...comment }));
  target.next = comments.length;
}

function leftover(old, previous) {
  const outcome = previous && old.submission && previous.submission === old.submission ? previous.end : null;
  if (old.stage === 'returned' || outcome === 'returned') return null;
  if (old.stage === 'accepted') return outcome === 'uncertain' ? resend(old.sent) : null;
  const comments = old.comments.map(comment => ({ text: comment.text, anchor: comment.anchor }));
  const index = old.comments.findIndex(comment => comment.id === old.editing);
  if (index !== -1 && old.editor.trim()) comments[index] = { text: old.editor.trim(), anchor: old.anchor };
  const editor = index === -1 ? { text: old.editor, anchor: old.anchor } : old.previous || { text: '', anchor: null };
  return { comments, editor: editor.text, anchor: editor.anchor, choices: old.choices, uncertain: old.uncertain || outcome === 'uncertain' };
}

function resend(batch) {
  return { comments: batch.comments, editor: '', anchor: null, choices: batch.choices, uncertain: true };
}

function carry(old, next) {
  const result = blank();
  const batch = leftover(old, next.previous);
  if (!batch) return result;
  for (const question of next.questions) {
    const option = batch.choices[question.id];
    if (option && question.options.includes(option)) result.choices[question.id] = option;
  }
  withComments(result, batch.comments);
  result.editor = batch.editor;
  result.anchor = batch.anchor;
  result.uncertain = batch.uncertain;
  result.carried = batch.comments.length > 0 || Boolean(batch.editor.trim()) || Object.keys(result.choices).length > 0;
  return result;
}

function save() {
  if (!view || !draft) return;
  try {
    const record = stored();
    if (record && record.view.token !== view.token && roundNumber(record.view.round) > roundNumber(view.round)) return;
    localStorage.setItem(RECORD_KEY, JSON.stringify({ view, draft }));
  } catch { }
}

function forget() {
  try { localStorage.removeItem(RECORD_KEY); } catch { }
  try {
    navigator.serviceWorker.getRegistration().then(registration => registration && registration.unregister()).catch(() => { });
    caches.delete(CACHE_NAME).catch(() => { });
  } catch { }
}

function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  if (className) node.className = className;
  return node;
}

function setText(node, text) {
  if (node.textContent !== text) node.textContent = text;
}

function collectComments() {
  const result = draft.comments.map(comment => ({ ...comment }));
  const text = editor.value.trim();
  if (draft.editing !== null) {
    const index = result.findIndex(comment => comment.id === draft.editing);
    if (index !== -1 && text) result[index] = { ...result[index], text, anchor: draft.anchor };
  } else if (text) {
    result.push({ id: 'draft', text, anchor: draft.anchor });
  }
  return result;
}

function encodedBytes(text) {
  const separators = (text.match(LINE_SEPARATORS) || []).length;
  return encoder.encode(JSON.stringify(text)).length - 2 + 3 * separators;
}

function commentBytes() {
  return collectComments().reduce((total, comment) => total + encodedBytes(comment.text), 0);
}

function sent() { return draft.stage !== ''; }

function frozen() { return sent() || sending || detached || inactive; }

function advance(stage) {
  if (draft.uncertain) return;
  if (STAGES.indexOf(stage) > STAGES.indexOf(draft.stage)) {
    draft.stage = stage;
    draft.carried = false;
    refusal = '';
  }
}

function noteAcceptedWhileLive() {
  if (live && draft && draft.stage === 'accepted') acceptedWhileLive = draft.submission;
}

function markUncertain() {
  if (!draft || draft.stage !== 'accepted' || draft.submission !== acceptedWhileLive) return;
  const batch = resend(draft.sent);
  draft.stage = '';
  draft.uncertain = batch.uncertain;
  draft.choices = { ...batch.choices };
  withComments(draft, batch.comments);
  draft.anchor = batch.anchor;
  draft.editing = null;
  draft.previous = null;
  draft.editor = batch.editor;
  draft.sent = null;
  editor.value = '';
  syncChoices();
  save();
}

function syncChoices() {
  for (const input of document.querySelectorAll('#document input[type=radio]')) {
    input.checked = draft.choices[input.dataset.question] === input.value;
  }
}

function phase() {
  if (closed) return 'closed';
  if (detached) return 'detached';
  if (!view) return 'connecting';
  if (draft.stage === 'returned') return live ? 'returned' : 'waiting';
  if (draft.stage === 'accepted' && !live && draft.submission !== acceptedWhileLive) return 'unconfirmed';
  if (draft.stage) return draft.stage;
  if (sending) return 'sending';
  if (inactive) return 'inactive';
  if (live) return 'user';
  return draft.uncertain ? 'uncertain' : 'reconnecting';
}

function kilobytes(bytes) {
  return (bytes / 1024).toFixed(1).replace('.', ',') + ' KB';
}

function countLabel(count) {
  return count + (count === 1 ? ' commento' : ' commenti');
}

function questionTitle(id) {
  const legend = document.getElementById('q-' + id);
  return legend ? legend.textContent : id;
}

function optionLabel(id, option) {
  const input = document.querySelector(`#document input[data-question="${id}"][value="${option}"]`);
  return input ? input.closest('label').querySelector('strong').textContent : option;
}

function choiceItem(question, option) {
  const item = element('li', undefined, option ? 'review-choice' : 'review-choice missing');
  item.append(element('span', questionTitle(question.id), 'review-question'));
  item.append(element('span', option ? optionLabel(question.id, option) : 'nessuna scelta', 'review-answer'));
  return item;
}

function reviewItems(comments) {
  const items = view.questions.map(question => choiceItem(question, draft.choices[question.id]));
  for (const comment of comments) {
    const item = element('li', undefined, 'review-comment review-line');
    if (comment.anchor) item.append(element('span', comment.anchor, 'review-anchor'));
    item.append(element('span', comment.text, 'review-text'));
    if (comment.id === 'draft') item.append(element('span', 'nell’editor', 'review-marker'));
    items.push(item);
  }
  return items;
}

function sentItems(batch) {
  const items = view.questions.filter(question => batch.choices[question.id])
    .map(question => choiceItem(question, batch.choices[question.id]));
  for (const comment of batch.comments) {
    const item = element('li', undefined, 'review-comment');
    if (comment.anchor) item.append(element('span', comment.anchor, 'note-anchor'));
    item.append(element('span', comment.text));
    items.push(item);
  }
  return items;
}

function frame() { return $('#content'); }

function toFrame(message) {
  const content = frame();
  if (content && content.contentWindow) content.contentWindow.postMessage(message, '*');
}

function setPicking(active) {
  picking = active && Boolean(frame());
  const button = $('#pick-anchor');
  button.setAttribute('aria-pressed', String(picking));
  setText(button, picking ? 'Annulla collegamento' : 'Collega un punto della pagina');
  $('#picker-hint').hidden = !picking;
  toFrame({ lavagna: 'pick', active: picking });
  if (picking) frame().focus({ preventScroll: true });
}

function updateAnchor(locked) {
  const anchorable = Boolean(view.frame) && view.anchors.length > 0;
  $('#anchor-line').hidden = !anchorable && !draft.anchor;
  $('#pick-anchor').disabled = locked || !anchorable;
  $('#clear-anchor').hidden = !draft.anchor;
  $('#clear-anchor').disabled = locked;
  setText($('#anchor-label'), draft.anchor ? 'Riferimento: ' + draft.anchor : '');
  if (picking && locked) setPicking(false);
  toFrame({ lavagna: 'mark', ref: draft.anchor });
}

function layout(data) {
  const height = Number(data.height);
  if (Number.isFinite(height) && height >= 0) frame().style.height = Math.min(Math.ceil(height), MAX_FRAME_HEIGHT) + 'px';
  chapterTops = {};
  const tops = data.chapters && typeof data.chapters === 'object' ? data.chapters : {};
  for (const chapter of view.chapters) {
    const top = Number(tops[chapter.id]);
    if (Object.hasOwn(tops, chapter.id) && Number.isFinite(top) && !document.getElementById(chapter.id)) chapterTops[chapter.id] = Math.max(0, top);
  }
  toFrame({ lavagna: 'pick', active: picking });
  toFrame({ lavagna: 'mark', ref: draft.anchor });
  updateRoute();
}

function fromFrame(event) {
  const content = frame();
  const data = event.data;
  if (!view || !content || event.source !== content.contentWindow || !data || typeof data !== 'object') return;
  if (data.lavagna === 'layout') layout(data);
  if (data.lavagna === 'anchor' && picking && !frozen() && typeof data.ref === 'string' && view.anchors.includes(data.ref)) {
    draft.anchor = data.ref;
    setPicking(false);
    $('#editor-status').textContent = 'Punto collegato. Scrivi il commento qui sotto.';
    changed();
    editor.focus();
  }
  if (data.lavagna === 'cancel' && picking) cancelPicking();
}

function cancelPicking() {
  setPicking(false);
  $('#editor-status').textContent = 'Collegamento annullato. Il commento è rimasto intatto.';
  $('#pick-anchor').focus();
}

function sendBlocker(over, count, answers) {
  if (!live) return 'Lavagna non è collegata: il feedback resta in bozza.';
  if (draft.editing !== null) return 'Salva o annulla la modifica per inviare.';
  if (over) return 'Accorcia i commenti per inviare.';
  if (!answers && count === 0) return 'Scegli un’opzione o scrivi un commento per inviare.';
  return '';
}

function updateTurn() {
  const current = phase();
  const turn = $('#turn');
  turn.dataset.state = current;
  setText(turn, PHASES[current].turn);
}

function updateControls() {
  updateTurn();
  if (!view) return;
  const current = PHASES[phase()];
  const isSent = sent();
  const panelName = current.panel === 'detached' && isSent ? 'sent' : current.panel;
  const panel = PANELS[panelName];
  const locked = frozen();
  const comments = collectComments();
  const answers = Object.keys(draft.choices).length;
  const bytes = commentBytes();
  const over = bytes > view.limit;

  $('#feedback').dataset.phase = panelName;
  setText($('#feedback-eyebrow'), panel.eyebrow);
  setText($('#feedback-lead'), current.lead || panel.lead);

  editor.readOnly = locked;
  updateAnchor(locked);
  for (const input of document.querySelectorAll('#document input[type=radio]')) input.disabled = locked;
  $('#add-comment').disabled = locked || !editor.value.trim();
  setText($('#add-comment'), draft.editing === null ? 'Aggiungi commento' : 'Salva modifica');
  $('#cancel-edit').hidden = draft.editing === null;
  $('#cancel-edit').disabled = isSent || sending;

  const counter = $('#comment-counter');
  counter.hidden = bytes < view.limit * COUNTER_FROM;
  counter.classList.toggle('over', over);
  setText(counter, over
    ? `${kilobytes(bytes)} di ${kilobytes(view.limit)}: il testo supera il limite. Accorcia i commenti per inviare; nulla viene tagliato.`
    : `${kilobytes(bytes)} di ${kilobytes(view.limit)} disponibili per i commenti`);

  const showSent = isSent && draft.sent;
  $('#draft-area').hidden = showSent;
  $('#sent-area').hidden = !showSent;
  $('#review').hidden = isSent;
  if (showSent) $('#sent-list').replaceChildren(...sentItems(draft.sent));
  else $('#review-list').replaceChildren(...reviewItems(comments));

  const blocked = sendBlocker(over, comments.length, answers);
  const button = $('#send-feedback');
  button.disabled = locked || Boolean(blocked);
  button.hidden = isSent;
  setText(button, sending ? 'Invio in corso…' : 'Invia feedback');
  setText($('#send-hint'), isSent || sending || detached || inactive ? '' : blocked);

  const delivery = $('#delivery');
  const message = current.delivery || refusal || (draft.uncertain ? TEXT.uncertain : '');
  setText(delivery, message);
  delivery.classList.toggle('refused', !current.delivery && Boolean(message) || current.panel === 'detached');
  delivery.dataset.stage = panelName === 'sent' && current.panel !== 'detached' ? draft.stage : '';

  updateMobileBar(answers, comments.length);
}

function updateMobileBar(answers, count) {
  const bar = $('#mobile-bar');
  bar.hidden = !view || closed || !narrow.matches || feedbackVisible;
  if (!view) return;
  setText($('#to-feedback'), sent() ? 'Vedi riepilogo' : 'Rivedi e invia');
  const parts = [];
  if (answers) parts.push(answers + (answers === 1 ? ' risposta' : ' risposte'));
  if (count) parts.push(countLabel(count));
  setText($('#mobile-summary'), !sent() && parts.length ? parts.join(' · ') + ' in bozza' : PHASES[phase()].turn);
}

function renderComments() {
  const list = $('#comments');
  list.replaceChildren();
  for (const comment of draft.comments) {
    const item = element('li', undefined, 'note' + (comment.id === draft.editing ? ' editing' : ''));
    if (comment.anchor) item.append(element('p', comment.anchor, 'note-anchor'));
    item.append(element('p', comment.id === draft.editing ? 'Stai modificando questo commento nell’editor.' : comment.text));
    const actions = element('div', undefined, 'note-actions');
    const edit = element('button', 'Modifica');
    edit.type = 'button';
    edit.disabled = draft.editing !== null || frozen();
    edit.addEventListener('click', () => {
      draft.previous = { text: editor.value, anchor: draft.anchor };
      draft.editing = comment.id;
      draft.anchor = comment.anchor;
      editor.value = comment.text;
      $('#editor-status').textContent = 'Modifica il commento; non ne verrà creata una copia.';
      changed();
      renderComments();
      editor.focus();
    });
    const remove = element('button', 'Rimuovi');
    remove.type = 'button';
    remove.disabled = draft.editing !== null || frozen();
    remove.addEventListener('click', () => {
      draft.comments = draft.comments.filter(other => other.id !== comment.id);
      $('#editor-status').textContent = 'Commento rimosso dalla bozza.';
      changed();
      renderComments();
    });
    actions.append(edit, remove);
    item.append(actions);
    list.append(item);
  }
}

function changed() {
  draft.editor = editor.value;
  draft.carried = false;
  if (!sent()) draft.submission = null;
  if (!inactive) refusal = '';
  save();
  updateControls();
}

function restorePrevious() {
  editor.value = draft.previous ? draft.previous.text : '';
  draft.anchor = draft.previous ? draft.previous.anchor : null;
  draft.previous = null;
  draft.editing = null;
}

function renderRoute() {
  const route = $('#route');
  route.replaceChildren();
  for (const chapter of view.chapters) {
    const link = element('a', undefined, chapter.role);
    link.href = '#' + chapter.id;
    link.append(element('span', chapter.index), ' ' + chapter.name);
    link.addEventListener('click', event => {
      const top = chapterTop(chapter.id);
      if (top === null) return;
      event.preventDefault();
      const offset = $('#topbar').getBoundingClientRect().height + 20;
      window.scrollTo({ top: window.scrollY + top - offset, behavior: 'instant' });
    });
    const item = element('li');
    item.append(link);
    route.append(item);
  }
  updateRoute();
}

function updateRoute() {
  if (!view) return;
  const offset = $('#topbar').getBoundingClientRect().height + 40;
  const scrollable = document.documentElement.scrollHeight - window.innerHeight;
  const progress = scrollable > 0 ? Math.min(1, window.scrollY / scrollable) : 0;
  const line = offset + (window.innerHeight - offset) * progress;
  let current = view.chapters.length ? view.chapters[0].id : '';
  for (const chapter of view.chapters) {
    const top = chapterTop(chapter.id);
    if (top !== null && top <= line) current = chapter.id;
  }
  for (const link of document.querySelectorAll('#route a')) {
    if (link.hash === '#' + current) link.setAttribute('aria-current', 'location');
    else link.removeAttribute('aria-current');
  }
}

function chapterTop(id) {
  const content = frame();
  if (content && Object.hasOwn(chapterTops, id)) return content.getBoundingClientRect().top + content.clientTop + chapterTops[id];
  const section = document.getElementById(id);
  return section ? section.getBoundingClientRect().top : null;
}

function dataURL(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(blob);
  });
}

async function cacheFrame(next) {
  await navigator.serviceWorker.ready;
  const cache = await caches.open(CACHE_NAME);
  const resources = new Map();
  for (const path of next.resources) {
    const url = new URL(path, location.origin).href;
    let response = await cache.match(url);
    if (!response) {
      response = await fetch(url);
      if (!response.ok) throw new Error('Round resource unavailable');
      await cache.put(url, response.clone());
    }
    resources.set(url, response);
  }
  if (live) return null;
  const encoded = new Map();
  const scripts = new Map();
  for (const [url, response] of resources) {
    if (url.endsWith('.css') || url === new URL(next.frame, location.origin).href) continue;
    const blob = await response.clone().blob();
    encoded.set(url, await dataURL(blob));
    if (url.endsWith('.js')) {
      const digest = await crypto.subtle.digest('SHA-256', await blob.arrayBuffer());
      scripts.set(url, 'sha256-' + btoa(String.fromCharCode(...new Uint8Array(digest))));
    }
  }
  for (const [url, response] of resources) {
    if (!url.endsWith('.css')) continue;
    const css = (await response.text()).replace(/url\(\s*(['"]?)([^)'"\s]+)\1\s*\)/g, (match, quote, path) => {
      const data = encoded.get(new URL(path, url).href);
      return data ? 'url("' + data + '")' : match;
    });
    encoded.set(url, await dataURL(new Blob([css], { type: 'text/css' })));
  }
  const url = new URL(next.frame, location.origin).href;
  const response = resources.get(url);
  const doc = new DOMParser().parseFromString(await response.text(), 'text/html');
  for (const node of doc.querySelectorAll('[src], [href]')) {
    for (const attr of ['src', 'href']) {
      if (!node.hasAttribute(attr)) continue;
      const resource = new URL(node.getAttribute(attr), url).href;
      const data = encoded.get(resource);
      if (data) node.setAttribute(attr, data);
      if (node.tagName === 'SCRIPT' && scripts.has(resource)) node.setAttribute('integrity', scripts.get(resource));
    }
  }
  const policy = doc.createElement('meta');
  policy.httpEquiv = 'Content-Security-Policy';
  // A meta policy cannot carry sandbox or frame-ancestors. The iframe keeps
  // its sandbox attribute, and the shell restricts frame navigation.
  policy.content = response.headers.get('Content-Security-Policy')
    .replace(/(?:sandbox|frame-ancestors)[^;]*;?\s*/g, '')
    .replace("script-src 'self'", 'script-src ' + [...scripts.values()].map(hash => "'" + hash + "'").join(' '))
    .replace("style-src 'self'", "style-src 'self' data:")
    .replace("font-src 'self'", "font-src 'self' data:");
  doc.head.prepend(policy);
  return URL.createObjectURL(new Blob(['<!doctype html>' + doc.documentElement.outerHTML], { type: 'text/html' }));
}

async function render(next) {
  const record = stored();
  const own = record && record.view.token === next.token;
  draft = own ? record.draft : record ? carry(record.draft, next) : blank();
  view = next;
  sending = false;
  inactive = false;
  refusal = '';
  picking = false;
  chapterTops = {};
  $('#picker-hint').hidden = true;
  let snapshot = null;
  if (view.frame) {
    try {
      snapshot = await cacheFrame(next);
    } catch { }
    if (view !== next) {
      if (snapshot) URL.revokeObjectURL(snapshot);
      return;
    }
  }
  const area = $('#document');
  area.replaceChildren();
  if (snapshotURL) URL.revokeObjectURL(snapshotURL);
  snapshotURL = !live ? snapshot : null;
  if (live && snapshot) URL.revokeObjectURL(snapshot);
  if (view.frame) {
    const content = document.createElement('iframe');
    content.id = 'content';
    content.className = 'content-frame';
    content.title = 'Contenuto del round';
    content.setAttribute('sandbox', 'allow-scripts');
    content.src = snapshotURL || view.frame;
    area.append(content);
  }
  area.insertAdjacentHTML('beforeend', view.decide);
  setText($('#round-label'), 'Round ' + view.round.replace(/^r/, ''));
  document.title = 'lavagna · round ' + view.round.replace(/^r/, '');
  for (const input of document.querySelectorAll('#document input[type=radio]')) {
    input.checked = draft.choices[input.dataset.question] === input.value;
    input.addEventListener('change', () => {
      draft.choices[input.dataset.question] = input.value;
      changed();
    });
  }
  editor.value = draft.editor;
  $('#editor-status').textContent = draft.carried ? TEXT.carried : '';
  $('#waiting').hidden = true;
  $('#closed').hidden = true;
  form.hidden = false;
  renderRoute();
  renderComments();
  if (!own) save();
  updateControls();
}

function newSubmission() {
  const bytes = crypto.getRandomValues(new Uint8Array(12));
  return 's-' + Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
}

async function send() {
  if (!view || $('#send-feedback').disabled) return;
  draft.submission = draft.submission || newSubmission();
  const comments = collectComments().map(comment => ({ text: comment.text, anchor: comment.anchor || null }));
  const choices = { ...draft.choices };
  draft.sent = { choices, comments };
  draft.uncertain = false;
  acceptedWhileLive = draft.submission;
  sending = true;
  refusal = '';
  save();
  renderComments();
  updateControls();
  try {
    const response = await fetch('send', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ round: view.round, token: view.token, submission: draft.submission, choices, comments }),
    });
    const body = await response.json().catch(() => ({}));
    if (response.status === 202 || response.status === 200) {
      advance(body.stage || 'accepted');
      withComments(draft, comments);
      draft.editing = null;
      draft.anchor = null;
      editor.value = '';
      draft.editor = '';
    } else if (response.status === 409) {
      inactive = true;
      refusal = body.error || String(response.status);
    } else {
      refusal = 'Invio rifiutato da lavagna: ' + (body.error || response.status) + '. La bozza è conservata.';
    }
  } catch {
    if (!sent()) refusal = 'Invio non riuscito: lavagna non risponde. La bozza è conservata.';
  } finally {
    sending = false;
    if (!live) markUncertain();
    save();
    renderComments();
    updateControls();
  }
}

editor.addEventListener('input', () => {
  $('#editor-status').textContent = '';
  changed();
});

editor.addEventListener('keydown', event => {
  if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
    event.preventDefault();
    send();
  }
});

$('#add-comment').addEventListener('click', () => {
  const text = editor.value.trim();
  if (!text) return;
  if (draft.editing !== null) {
    const comment = draft.comments.find(item => item.id === draft.editing);
    if (comment) Object.assign(comment, { text, anchor: draft.anchor });
    restorePrevious();
  } else {
    draft.comments.push({ id: ++draft.next, text, anchor: draft.anchor });
    draft.anchor = null;
    editor.value = '';
  }
  $('#editor-status').textContent = 'Commento nella bozza. Non è stato inviato.';
  changed();
  renderComments();
  editor.focus();
});

$('#cancel-edit').addEventListener('click', () => {
  restorePrevious();
  $('#editor-status').textContent = 'Modifica annullata; la bozza precedente è rimasta intatta.';
  changed();
  renderComments();
});

$('#to-feedback').addEventListener('click', () => {
  const button = $('#send-feedback');
  const target = sent() ? $('#sent-area') : button.disabled ? $('#review') : button;
  button.closest('.send-area').scrollIntoView({ block: 'end' });
  target.focus({ preventScroll: true });
});

$('#pick-anchor').addEventListener('click', () => {
  if (picking) {
    cancelPicking();
    return;
  }
  setPicking(true);
  $('#editor-status').textContent = 'Scegli un punto evidenziato nel contenuto.';
});

$('#clear-anchor').addEventListener('click', () => {
  draft.anchor = null;
  $('#editor-status').textContent = 'Riferimento rimosso. Il commento vale per la pagina nel suo insieme.';
  changed();
  editor.focus();
});

document.addEventListener('keydown', event => {
  if (event.key === 'Escape' && picking) {
    event.preventDefault();
    cancelPicking();
  }
});

window.addEventListener('message', fromFrame);

form.addEventListener('submit', event => {
  event.preventDefault();
  send();
});

window.addEventListener('scroll', updateRoute, { passive: true });
narrow.addEventListener('change', () => updateControls());
new IntersectionObserver(entries => {
  feedbackVisible = entries[entries.length - 1].isIntersecting;
  if (view) updateControls();
}).observe($('#feedback'));
new ResizeObserver(() => {
  document.documentElement.style.setProperty('--header-height', $('#topbar').getBoundingClientRect().height + 'px');
}).observe($('#topbar'));

function showClosed() {
  closed = true;
  live = false;
  forget();
  view = null;
  form.hidden = true;
  $('#waiting').hidden = true;
  $('#closed').hidden = false;
  $('#route').replaceChildren();
  setText($('#round-label'), '');
  $('#mobile-bar').hidden = true;
  updateTurn();
}

function showDetachedWaiting() {
  setText($('#waiting .notice-lead'), TEXT.detached);
  setText($('#waiting .muted'), 'Riapri la pagina dal link nel terminale.');
}

function adopt() {
  const record = stored();
  if (!view || !record || record.view.token !== view.token) return;
  draft = record.draft;
  editor.value = draft.editor;
  syncChoices();
  noteAcceptedWhileLive();
  renderComments();
  updateControls();
}

function connect() {
  const source = new EventSource('events');
  source.addEventListener('round', event => {
    const next = JSON.parse(event.data);
    live = true;
    if (!view || view.token !== next.token) render(next);
    noteAcceptedWhileLive();
    updateControls();
  });
  source.addEventListener('receipt', event => {
    const receipt = JSON.parse(event.data);
    if (!view || !draft || receipt.submission !== draft.submission) return;
    advance(receipt.stage);
    noteAcceptedWhileLive();
    save();
    updateControls();
  });
  source.addEventListener('closed', () => {
    source.close();
    showClosed();
  });
  source.addEventListener('error', () => {
    if (leaving) return;
    live = false;
    if (source.readyState === EventSource.CLOSED) detached = true;
    else {
      source.close();
      setTimeout(connect, RETRY_MS);
    }
    if (view && !sending) markUncertain();
    if (view) {
      renderComments();
      updateControls();
    } else {
      if (detached) showDetachedWaiting();
      updateTurn();
    }
  });
}

window.addEventListener('pagehide', () => { leaving = true; });
window.addEventListener('pageshow', () => { leaving = false; });

window.addEventListener('storage', event => {
  if (event.key === RECORD_KEY) adopt();
});

try { navigator.serviceWorker.register('sw.js').catch(() => { }); } catch { }
const restored = stored();
if (restored) render(restored.view);
connect();
