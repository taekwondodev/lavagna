'use strict';

const TEXT = {
  accepted: 'Ricevuto da lavagna · non ancora consegnato all’agente',
  returned: 'Consegnato al terminale',
  received: 'Letto dall’agente · l’agente lavora',
  read: 'Letto dall’agente',
  unread: 'Consegnato al terminale, ma il turno è terminato prima che l’agente lo leggesse.',
  'unread-aborted': 'Consegnato al terminale, ma il turno si è interrotto prima che l’agente lo leggesse.',
  ended: 'L’agente ha terminato il turno prima di chiudere la frontiera. Il tuo feedback è conservato.',
  aborted: 'Il turno dell’agente è stato interrotto. La frontiera non è stata chiusa e il tuo feedback è conservato.',
  unwitnessed: 'stato in tempo reale non disponibile',
  detached: 'Questa scheda non è più collegata alla conversazione',
  closed: 'Frontiera chiusa, torna al terminale',
};
// Each stage: the short form in the title bar and the full sentence in the
// footer. A null sentence shows the draft summary instead.
const STAGES = {
  connecting: ['Collegamento a lavagna…', ''],
  user: ['Tocca a te', null],
  reconnecting: ['Riconnessione a lavagna…', ''],
  sending: ['Invio in corso…', 'Invio in corso…'],
  inactive: ['Round non più attivo', ''],
  accepted: ['Inviato', TEXT.accepted],
  returned: ['Consegnato al terminale', TEXT.returned],
  waiting: ['Consegnato al terminale', TEXT.returned],
  received: ['L’agente lavora', TEXT.received],
  read: ['Letto dall’agente', TEXT.read],
  unread: ['Grilling ancora aperto', TEXT.unread],
  'unread-aborted': ['Turno interrotto', TEXT['unread-aborted']],
  ended: ['Grilling ancora aperto', TEXT.ended],
  aborted: ['Turno interrotto', TEXT.aborted],
  detached: ['Scheda non collegata', TEXT.detached],
  closed: ['Concluso', TEXT.closed],
};
const ORDER = ['', 'accepted', 'returned', 'received', 'unread', 'ended', 'aborted', 'unread-aborted'];
const DEGRADED = ['returned', 'waiting', 'read'];
const OVERVIEW = ':overview';
const RECORD_KEY = 'lavagna:' + location.pathname;
const CACHE_NAME = 'lavagna:' + location.pathname;
const RETRY_MS = 250;
const COUNTER_FROM = 0.75;
const MAX_FRAME_HEIGHT = 100000;
const LINE_SEPARATORS = new RegExp('[' + String.fromCharCode(0x2028, 0x2029) + ']', 'g');

const $ = selector => document.querySelector(selector);
const composer = $('#composer');
// At phone width the rail is a strip, the discussion a sheet and Expand shows
// the block full screen at its own size. lavagna.css uses the same width.
const PHONE = matchMedia('(max-width: 640px)');
const encoder = new TextEncoder();

// view is the server's last view of the phase. record is the phase's draft in
// localStorage: unsent choices, answers, messages and screenshots, seen marks,
// and the batch sent in the current call until the server's threads hold it.
let view = null;
let record = null;
let active = null;
let content = null;
let centerKey = '';
let frameKey = '';
let threadKey = '';
let live = false;
let detached = false;
let closed = false;
let inactive = false;
let sending = false;
let leaving = false;
let refusal = '';
let uploading = 0;
let imageRefusal = '';
let snapshotURL = null;
let expanded = false;
let sheet = false;
const renders = new Set();

function blank() {
  return { questions: {}, overview: thread(), seen: {}, heard: {}, batch: null, delivery: null, submission: null };
}

function thread() { return { messages: [], images: [], composer: '' }; }

function stored() {
  try {
    const value = JSON.parse(localStorage.getItem(RECORD_KEY));
    if (value && value.questions && value.overview && value.seen) return value;
  } catch { }
  return null;
}

// save stores the record. A batch stays stored only until it is returned:
// until then a reload or a lost call needs it; afterwards it is delivered and
// only this tab shows it until the server's threads hold it.
function save() {
  if (!record) return;
  const delivered = record.delivery && ORDER.indexOf(record.delivery.stage) >= ORDER.indexOf('returned');
  try { localStorage.setItem(RECORD_KEY, JSON.stringify(delivered ? { ...record, batch: null } : record)); } catch { }
}

function track(task) {
  renders.add(task);
  task.then(() => renders.delete(task), () => renders.delete(task));
  return task;
}

// ---- reading the view ----

function questions() { return view ? view.phase.questions : []; }

function question(id) { return questions().find(q => q.id === id) || null; }

function number(id) {
  const index = questions().findIndex(q => q.id === id);
  return index === -1 ? id : 'Q' + (index + 1);
}

function label(id) { return id === OVERVIEW ? 'Panoramica' : number(id); }

function answerable(q) { return q.status === 'open' && q.round === view.phase.round; }

function serverThread(id) {
  if (id === OVERVIEW) return view.phase.overview;
  const q = question(id);
  return q ? q.thread : [];
}

function agentCount(id) { return serverThread(id).filter(m => m.author === 'agent').length; }

function received(submission) {
  const from = m => m.author === 'user' && m.submission === submission;
  return view.phase.overview.some(from) || questions().some(q => q.thread.some(from));
}

function key(q, answer) {
  if (answer.choice) {
    const index = q.options.findIndex(o => o.id === answer.choice);
    return index === -1 ? '?' : String.fromCharCode(65 + index);
  }
  return answer.text && answer.text.trim() ? '✎' : '';
}

// ---- the draft ----

function draftOf(id) {
  if (id === OVERVIEW) return record.overview;
  if (!record.questions[id]) record.questions[id] = thread();
  return record.questions[id];
}

function peek(id) { return id === OVERVIEW ? record.overview : record.questions[id] || null; }

function sameAnswer(a, b) {
  return (a.choice || '') === (b.choice || '') && (a.text || '').trim() === (b.text || '').trim();
}

function ledgerAnswer(q) {
  if (!q.answer) return {};
  return q.answer.choice ? { choice: q.answer.choice } : { text: q.answer.text || '' };
}

// The answer last sent: this call's batch when one is pending in the threads,
// else the ledger's recorded answer.
function baseline(q) {
  if (record.batch && answerable(q)) {
    const sent = record.batch.questions[q.id];
    if (!sent) return {};
    return sent.choice ? { choice: sent.choice } : sent.answer ? { text: sent.answer } : {};
  }
  return ledgerAnswer(q);
}

function answerOf(q) {
  if (!answerable(q)) return ledgerAnswer(q);
  const draft = peek(q.id);
  return draft && draft.answer ? draft.answer : baseline(q);
}

function stagedChoice(q) {
  const draft = peek(q.id);
  return answerable(q) && draft && draft.answer && !sameAnswer(draft.answer, baseline(q));
}

function stagedMessages(draft) {
  const texts = draft.messages.map(m => m.text);
  if (draft.composer.trim()) texts.push(draft.composer.trim());
  return texts;
}

function staged() {
  const items = [];
  const each = (id, draft) => {
    if (!draft) return;
    const messages = stagedMessages(draft).length;
    if (messages) items.push({ id, kind: 'msg', count: messages });
    if (draft.images.length) items.push({ id, kind: 'shot', count: draft.images.length });
  };
  for (const q of questions()) {
    if (stagedChoice(q)) items.push({ id: q.id, kind: 'choice', count: 1, key: key(q, answerOf(q)) || 'nessuna' });
    each(q.id, peek(q.id));
  }
  each(OVERVIEW, record.overview);
  return items;
}

function stagedCount(items) { return items.reduce((total, item) => total + item.count, 0); }

function encodedBytes(text) {
  const separators = (text.match(LINE_SEPARATORS) || []).length;
  return encoder.encode(JSON.stringify(text)).length - 2 + 3 * separators;
}

function textBytes() {
  let total = 0;
  for (const q of questions()) {
    const draft = peek(q.id);
    if (draft) for (const text of stagedMessages(draft)) total += encodedBytes(text);
    const answer = answerOf(q);
    if (answerable(q) && !answer.choice && answer.text && answer.text.trim()) total += encodedBytes(answer.text.trim());
  }
  for (const text of stagedMessages(record.overview)) total += encodedBytes(text);
  return total;
}

function imageCount() {
  let total = record.overview.images.length;
  for (const id in record.questions) total += record.questions[id].images.length;
  return total;
}

// reconcile adapts the phase draft to a new view: drafts of questions that no
// longer take an answer lose it, and a batch the threads now hold stops being
// shown from the draft. A batch from an earlier call that the ledger never
// received returns to the draft.
function reconcile() {
  for (const id of Object.keys(record.questions)) {
    const q = question(id);
    if (!q) {
      delete record.questions[id];
      continue;
    }
    const draft = record.questions[id];
    if (draft.answer && (!answerable(q) || draft.answer.choice && !q.options.some(o => o.id === draft.answer.choice))) delete draft.answer;
  }
  const batch = record.batch;
  if (batch && received(batch.submission)) record.batch = null;
  if (record.delivery && record.delivery.call !== view.call) {
    if (record.batch) restore(record.batch);
    record.batch = null;
    record.delivery = null;
    record.submission = null;
    inactive = false;
    refusal = '';
  }
}

function restore(batch) {
  const back = (id, sent) => {
    if (id !== OVERVIEW && !question(id)) return;
    const draft = draftOf(id);
    draft.messages = (sent.messages || []).map(text => ({ text })).concat(draft.messages);
    draft.images = (sent.images || []).concat(draft.images);
    const q = question(id);
    if (q && answerable(q) && !draft.answer && (sent.choice || sent.answer)) draft.answer = sent.choice ? { choice: sent.choice } : { text: sent.answer };
  };
  for (const id in batch.questions) back(id, batch.questions[id]);
  back(OVERVIEW, batch.overview);
}

// changed records a draft edit. Composer text never shows on the rail, so
// typing a message leaves the rail alone.
function changed(rail = true) {
  if (!record.delivery || !record.delivery.stage) record.submission = null;
  if (!inactive) refusal = '';
  save();
  if (rail) renderRail();
  updateFooter();
}

// ---- stages ----

function delivery() { return record && record.delivery && view && record.delivery.call === view.call ? record.delivery : null; }

function sentThisCall() { return Boolean(delivery() && delivery().stage); }

function frozen() { return sending || detached || closed || !view; }

function phase() {
  if (closed) return 'closed';
  if (detached) return 'detached';
  if (!view) return 'connecting';
  if (sending) return 'sending';
  const d = delivery();
  if (d && d.stage) {
    if (d.stage === 'returned') return live ? 'returned' : 'waiting';
    if (d.stage === 'received') return live && !d.unwitnessed ? 'received' : 'read';
    if (d.stage === 'accepted' && !live) return 'reconnecting';
    return d.stage;
  }
  if (inactive) return 'inactive';
  return live ? 'user' : 'reconnecting';
}

function advance(receipt) {
  const d = delivery();
  if (!d) return;
  if (receipt.unwitnessed) d.unwitnessed = true;
  if (ORDER.indexOf(receipt.stage) > ORDER.indexOf(d.stage)) d.stage = receipt.stage;
}

// ---- small DOM helpers ----

function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined) node.append(text);
  if (className) node.className = className;
  return node;
}

function setText(node, text) {
  if (node.textContent !== text) node.textContent = text;
}

function button(text, className) {
  const node = element('button', text, className);
  node.type = 'button';
  return node;
}

// fmt renders agent text as text nodes, with `code` and **bold** spans: the
// shell never parses agent HTML.
function fmt(text) {
  const fragment = document.createDocumentFragment();
  for (const part of String(text).split(/(`[^`]+`|\*\*[^*]+\*\*)/)) {
    if (!part) continue;
    if (part.startsWith('`') && part.endsWith('`') && part.length > 1) fragment.append(element('code', part.slice(1, -1)));
    else if (part.startsWith('**') && part.endsWith('**') && part.length > 4) fragment.append(element('strong', part.slice(2, -2)));
    else fragment.append(part);
  }
  return fragment;
}

function paragraphs(text, className) {
  return String(text).split('\n').filter(line => line.trim()).map(line => element('p', fmt(line), className));
}

function size(bytes) {
  if (bytes < 1024 * 1024) return kilobytes(bytes);
  return (bytes / 1024 / 1024).toLocaleString('it-IT', { maximumFractionDigits: 1 }) + ' MB';
}

function kilobytes(bytes) {
  return (bytes / 1024).toFixed(1).replace('.', ',') + ' KB';
}

function plural(count, one, many) { return count + ' ' + (count === 1 ? one : many); }

// ---- title bar ----

function renderTitle() {
  const name = phase();
  const turn = $('#turn');
  turn.dataset.state = name;
  setText(turn, STAGES[name][0]);
  if (!view) return;
  setText($('#phase-title'), view.phase.title || '');
  setText($('#round-label'), view.round);
  document.title = 'lavagna · ' + (view.phase.title || 'round ' + view.phase.round);
}

// ---- rail ----

function seen(q) { return record.seen[q.id] === q.version; }

function newReply(id) { return agentCount(id) > (record.heard[id] || 0); }

function messageCount(id) { return threadEntries(id).length; }

function renderRail() {
  if (!view) return;
  const rail = $('#rail');
  const groups = [overviewCard()];
  const current = view.phase.round;
  for (let n = 1; n <= current; n++) groups.push(roundGroup(n, current));
  const planned = questions().filter(q => q.status === 'planned');
  if (planned.length) groups.push(plannedGroup(current + 1, planned));
  groups.push(legend());
  rail.replaceChildren(...groups);
}

function overviewCard() {
  const card = button(undefined, 'card card-overview');
  card.dataset.target = OVERVIEW;
  card.title = 'Panoramica e decisioni prese';
  if (active === OVERVIEW) card.setAttribute('aria-current', 'true');
  card.append(element('span', undefined, 'card-n card-icon'), element('span', 'Panoramica e decisioni prese', 'card-title'));
  const meta = element('span', undefined, 'card-meta');
  if (newReply(OVERVIEW)) meta.append(dot());
  const count = messageCount(OVERVIEW);
  if (count) meta.append(element('span', String(count), 'card-messages'));
  meta.append(element('span', String(view.phase.decisions.filter(d => !d.struck).length), 'mark mark-done'));
  card.append(meta);
  card.addEventListener('click', () => show(OVERVIEW));
  return card;
}

function dot() {
  const node = element('span', undefined, 'card-new');
  node.title = 'nuova risposta';
  return node;
}

function roundGroup(n, current) {
  const group = element('section', undefined, 'round round-' + (n === current ? 'current' : 'closed'));
  const head = element('div', undefined, 'round-head');
  head.append(element('span', 'Round ' + n), element('span', n === current ? 'current' : 'chiuso', 'round-state'));
  group.append(head);
  for (const q of questions()) {
    if (q.status !== 'planned' && q.round === n) group.append(card(q));
    (q.marks || []).forEach((mark, i) => {
      if (mark.round !== n || q.round === n) return;
      const target = q.marks[i + 1] ? q.marks[i + 1].round : q.round;
      group.append(ghost(q, mark.mark, target === current ? 'corrente' : target));
    });
  }
  return group;
}

function card(q) {
  const node = button(undefined, 'card');
  node.dataset.target = q.id;
  node.title = number(q.id) + ' · ' + q.title;
  if (active === q.id) node.setAttribute('aria-current', 'true');
  if (!seen(q)) node.classList.add('unseen');
  node.append(element('span', number(q.id), 'card-n'), element('span', q.title, 'card-title'));
  const meta = element('span', undefined, 'card-meta');
  if (q.status === 'open' && (q.marks || []).some(m => m.mark === 'reopened')) meta.append(element('span', 'riaperta', 'card-chip'));
  if (newReply(q.id)) meta.append(dot());
  const count = messageCount(q.id);
  if (count) meta.append(element('span', String(count), 'card-messages'));
  const answer = answerOf(q);
  if (q.status === 'settled') {
    meta.append(element('span', '✓ ' + key(q, answer), 'mark mark-done'));
  } else if (key(q, answer)) {
    meta.append(element('span', key(q, answer), 'mark ' + (stagedChoice(q) ? 'mark-staged' : 'mark-sent')));
  } else if (!seen(q)) {
    meta.append(element('span', undefined, 'mark mark-unseen'));
  }
  node.append(meta);
  node.addEventListener('click', () => show(q.id));
  return node;
}

function ghost(q, mark, target) {
  const node = element('div', undefined, 'card card-ghost');
  node.dataset.ghost = q.id;
  const where = target === 'corrente' ? 'nel round corrente' : 'nel round ' + target;
  node.append(element('span', number(q.id), 'card-n'), element('span', q.title, 'card-title'),
    element('span', (mark === 'reopened' ? 'riaperta ' : 'spostata ') + where, 'card-meta card-moved'));
  return node;
}

function plannedGroup(n, planned) {
  const group = element('section', undefined, 'round round-planned');
  const head = element('div', undefined, 'round-head');
  head.append(element('span', 'Round ' + n), element('span', 'previsto', 'round-state'));
  group.append(head);
  for (const q of planned) {
    const node = element('div', undefined, 'card card-locked');
    node.dataset.planned = q.id;
    node.append(element('span', number(q.id), 'card-n'), element('span', q.title, 'card-title'));
    const meta = element('span', undefined, 'card-meta');
    if (q.after && q.after.length) meta.append(element('span', 'dopo ' + q.after.map(number).join(', '), 'card-after'));
    node.append(meta);
    group.append(node);
  }
  group.append(element('p', 'Previsto dall’albero delle decisioni: può cambiare.', 'round-note'));
  return group;
}

function legend() {
  const node = element('div', undefined, 'legend');
  node.append(
    element('span', undefined, 'mark mark-unseen'), element('span', 'da vedere'),
    element('span', 'A', 'mark mark-staged'), element('span', 'scelta in bozza'),
    element('span', 'A', 'mark mark-sent'), element('span', 'scelta inviata'),
    element('span', '✓', 'mark mark-done'), element('span', 'decisa'),
    element('span', undefined, 'card-new'), element('span', 'nuova risposta'),
  );
  return node;
}

// ---- center ----

function defaultActive() {
  const current = questions().find(q => q.status !== 'planned' && q.round === view.phase.round);
  return current ? current.id : OVERVIEW;
}

function show(id) {
  if (id === active) return;
  active = id;
  composer.value = draftOf(id).composer;
  setText($('#composer-status'), '');
  imageRefusal = '';
  refreshViews();
  $('#center').scrollTop = 0;
}

function refreshViews() {
  markRead();
  renderRail();
  renderCenter();
  renderDiscussion();
  updateFooter();
  renderTitle();
}

function markRead() {
  if (active !== OVERVIEW) {
    const q = question(active);
    if (q) record.seen[q.id] = q.version;
  }
  record.heard[active] = agentCount(active);
  save();
}

// renderCenter redraws the head and 03 when the question's place in the phase
// changes, and the frame only when the question or its version changes, so a
// reply or a round advance leaves the frame and its prototype state alone.
function renderCenter() {
  const head = $('#question-head');
  const frameArea = $('#frame-area');
  const decideArea = $('#decide-area');
  const q = active === OVERVIEW ? null : question(active);
  const signature = JSON.stringify(q ? [q.id, q.version, q.status, q.round, q.after, view.phase.round] : [OVERVIEW, view.phase.title, view.phase.decisions]);
  const frameSignature = q ? JSON.stringify([q.id, q.version]) : '';
  if (frameSignature !== frameKey) {
    frameKey = frameSignature;
    destroyFrame();
    if (q && q.frame) mountFrame(q, frameArea);
  }
  if (signature === centerKey) {
    if (q) updateDecide(q);
    return;
  }
  centerKey = signature;
  if (!q) {
    head.replaceChildren(...overview());
    decideArea.replaceChildren();
    return;
  }
  const eyebrow = element('p', number(q.id) + ' · round ' + q.round + (q.after && q.after.length ? ' · dopo ' + q.after.map(number).join(', ') : ''), 'eyebrow');
  eyebrow.append(element('span', state(q), 'state state-' + (answerable(q) ? 'current' : 'closed')));
  const nodes = [eyebrow, element('h1', q.title, 'question-title'), ...paragraphs(q.lead || '', 'lead')];
  if (q.status === 'settled') {
    nodes.push(element('p', q.round < view.phase.round
      ? 'Round chiuso. Puoi rileggerla e commentarla a destra: il commento parte col prossimo invio e l’agente decide se riaprirla.'
      : 'Decisa in questo round. Puoi commentarla a destra: l’agente decide se riaprirla.', 'banner'));
  }
  head.replaceChildren(...nodes);
  decideArea.replaceChildren(decide(q));
  updateDecide(q);
}

function state(q) {
  if (q.status === 'settled') return q.round < view.phase.round ? 'chiuso · deciso ' + key(q, ledgerAnswer(q)) : 'deciso ' + key(q, ledgerAnswer(q));
  if ((q.marks || []).some(m => m.mark === 'reopened')) return 'riaperta';
  return 'current';
}

function overview() {
  const nodes = [element('p', 'Panoramica', 'eyebrow'), element('h1', view.phase.title || 'Panoramica', 'question-title'),
    element('p', 'Le decisioni prese stanno qui, in tabella. Il recap finale usa la stessa tabella.', 'lead')];
  const rows = view.phase.decisions;
  if (!rows.length) {
    nodes.push(element('p', 'Ancora nessuna decisione presa.', 'empty'));
    return nodes;
  }
  const table = element('table', undefined, 'decisions');
  const head = element('tr');
  for (const name of ['Domanda', 'Decisione', 'Round', 'Perché', 'Scartate']) head.append(element('th', name));
  const thead = element('thead');
  thead.append(head);
  const body = element('tbody');
  for (const row of rows) {
    const tr = element('tr', undefined, row.struck ? 'struck' : '');
    for (const text of [number(row.question) + ' · ' + row.title, row.decision, 'r' + row.round, row.why || '', (row.rejected || []).join(', ')]) {
      tr.append(element('td', fmt(text)));
    }
    body.append(tr);
  }
  table.append(thead, body);
  nodes.push(table);
  return nodes;
}

function decide(q) {
  const section = element('section', undefined, 'decide');
  section.id = 'decide';
  const head = element('header', undefined, 'decide-head');
  head.append(element('span', '03', 'chapter-index'), element('span', 'Decidere', 'chapter-name'),
    element('span', answerable(q) ? 'scegli o scrivi la tua' : 'deciso', 'chapter-purpose'));
  const list = element('div', undefined, 'options');
  list.setAttribute('role', 'group');
  list.setAttribute('aria-label', q.title);
  q.options.forEach((o, i) => {
    const option = button(undefined, 'option' + (o.recommended ? ' recommended' : ''));
    option.dataset.option = o.id;
    option.append(element('span', String.fromCharCode(65 + i), 'option-key'));
    const body = element('span', undefined, 'option-body');
    if (o.recommended) body.append(element('span', 'Consigliata', 'option-recommended'));
    body.append(element('span', fmt(o.label), 'option-label'));
    if (o.detail) body.append(element('span', fmt(o.detail), 'option-detail'));
    option.append(body);
    option.addEventListener('click', () => choose(q.id, o.id));
    list.append(option);
  });
  const answer = answerOf(q);
  if (answerable(q)) {
    const free = element('label', undefined, 'option option-free');
    const text = element('textarea');
    text.id = 'free-text';
    text.rows = 2;
    text.placeholder = 'Inserisci la tua risposta se nessuna opzione ti convince';
    text.setAttribute('aria-label', 'Risposta libera');
    const draft = peek(q.id);
    text.value = draft && draft.free !== undefined ? draft.free : answer.text || '';
    text.addEventListener('input', () => typeFree(q.id, text.value));
    free.append(element('span', '✎', 'option-key'), text);
    list.append(free);
  } else if (answer.text) {
    const free = element('div', undefined, 'option option-free on');
    free.append(element('span', '✎', 'option-key'), element('span', answer.text, 'option-label'));
    list.append(free);
  }
  section.append(head, list);
  if (q.reason) {
    const reason = element('p', undefined, 'reason');
    const recommended = q.options.findIndex(o => o.recommended);
    if (recommended !== -1) reason.append(element('strong', 'Perché ' + String.fromCharCode(65 + recommended) + '. '));
    reason.append(fmt(q.reason));
    section.append(reason);
  }
  return section;
}

function updateDecide(q) {
  const answer = answerOf(q);
  const editable = answerable(q) && !frozen();
  for (const option of document.querySelectorAll('#decide .option[data-option]')) {
    const on = answer.choice === option.dataset.option;
    option.classList.toggle('on', on);
    option.setAttribute('aria-pressed', String(on));
    option.disabled = !editable;
  }
  const text = $('#free-text');
  if (text) {
    text.readOnly = !editable;
    text.closest('.option').classList.toggle('on', !answer.choice && Boolean(answer.text && answer.text.trim()));
  }
  sendOption();
}

function choose(id, option) {
  const q = question(id);
  if (!q || !answerable(q) || frozen()) return;
  const draft = draftOf(id);
  draft.answer = answerOf(q).choice === option ? {} : { choice: option };
  changed();
  updateDecide(q);
}

function typeFree(id, value) {
  const q = question(id);
  if (!q || !answerable(q) || frozen()) return;
  const draft = draftOf(id);
  draft.free = value;
  draft.answer = value.trim() ? { text: value } : {};
  changed();
  updateDecide(q);
}

// ---- content frame ----

// One frame exists, for the active question. It is destroyed and recreated on
// every switch; while a question keeps its version, a new call keeps it.
function mountFrame(q, area) {
  const frame = element('iframe', undefined, 'content-frame');
  frame.id = 'content';
  frame.title = 'Contenuto della domanda';
  frame.setAttribute('sandbox', 'allow-scripts');
  content = { id: q.id, frame, ready: false };
  area.append(frame);
  if (live) {
    frame.src = q.frame;
    track(cacheQuestion(q)).catch(() => { });
    return;
  }
  track(snapshot(q)).then(url => {
    if (!content || content.frame !== frame) {
      URL.revokeObjectURL(url);
      return;
    }
    snapshotURL = url;
    frame.src = url;
  }, () => {
    if (content && content.frame === frame) frame.src = q.frame;
  });
}

function destroyFrame() {
  if (expanded) expand(false);
  $('#frame-area').replaceChildren();
  content = null;
  if (snapshotURL) URL.revokeObjectURL(snapshotURL);
  snapshotURL = null;
}

// The frame learns only the selected option and whether a free-text answer
// exists: never the free text, messages, screenshots, capability or token.
function sendOption() {
  if (!content || !content.ready || !content.frame.contentWindow) return;
  const q = question(content.id);
  if (!q) return;
  const answer = answerOf(q);
  const option = answer.choice || null;
  content.frame.contentWindow.postMessage({ lavagna: 'option', option, free: !option && Boolean(answer.text && answer.text.trim()) }, '*');
}

function fromFrame(event) {
  if (!content || event.source !== content.frame.contentWindow) return;
  const data = event.data;
  if (!data || typeof data !== 'object') return;
  if (data.lavagna === 'expand' && (data.active !== true || frameGesture())) expand(data.active === true);
  if (data.lavagna !== 'layout') return;
  const height = Number(data.height);
  if (Number.isFinite(height) && height >= 0) content.frame.style.height = Math.min(Math.ceil(height), MAX_FRAME_HEIGHT) + 'px';
  if (!content.ready) {
    content.ready = true;
    sendOption();
  }
}

// frameGesture tells whether the user is acting inside the frame: it holds
// focus and the page has transient activation. A round script alone cannot
// expand the page, so after Riduci it cannot expand it again.
function frameGesture() {
  return document.activeElement === content.frame && (!navigator.userActivation || navigator.userActivation.isActive);
}

// expand changes presentation only: rail and discussion, or at phone width the
// whole page chrome, hide while the active frame shows one wide block larger.
// At phone width the shell's own bar keeps Riduci, which frame code cannot
// remove. The frame learns the state once the layout has changed.
function expand(active) {
  const page = $('#page');
  expanded = active && Boolean(content);
  if (expanded) page.dataset.expanded = PHONE.matches ? 'native' : 'fit';
  else delete page.dataset.expanded;
  if (expanded) setText($('#expand-label'), label(content.id) + ' · schermata a grandezza reale');
  if (expanded && sheet) toggleSheet();
  if (!content || !content.frame.contentWindow) return;
  void page.offsetWidth;
  content.frame.contentWindow.postMessage({ lavagna: 'expanded', active: expanded, native: page.dataset.expanded === 'native' }, '*');
}

async function cacheQuestion(q) {
  await navigator.serviceWorker.ready;
  const cache = await caches.open(CACHE_NAME);
  for (const path of q.resources || []) {
    const url = new URL(path, location.origin).href;
    if (await cache.match(url)) continue;
    const response = await fetch(url);
    if (!response.ok) throw new Error('Question resource unavailable');
    await cache.put(url, response);
  }
}

function dataURL(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(blob);
  });
}

// snapshot rebuilds a cached question's frame document with its resources as
// data URLs and its scripts pinned by CSP hashes, for reading while nothing
// listens on the origin.
async function snapshot(q) {
  const cache = await caches.open(CACHE_NAME);
  const resources = new Map();
  for (const path of q.resources || []) {
    const url = new URL(path, location.origin).href;
    const response = await cache.match(url);
    if (!response) throw new Error('Question resource not cached');
    resources.set(url, response);
  }
  const frameURL = new URL(q.frame, location.origin).href;
  const encoded = new Map();
  const scripts = new Map();
  for (const [url, response] of resources) {
    if (response.headers.get('Content-Type').startsWith('text/css') || url === frameURL) continue;
    const blob = await response.clone().blob();
    encoded.set(url, await dataURL(blob));
    if (response.headers.get('Content-Type').startsWith('text/javascript')) {
      const digest = await crypto.subtle.digest('SHA-256', await blob.arrayBuffer());
      scripts.set(url, 'sha256-' + btoa(String.fromCharCode(...new Uint8Array(digest))));
    }
  }
  for (const [url, response] of resources) {
    if (!response.headers.get('Content-Type').startsWith('text/css')) continue;
    const css = (await response.clone().text()).replace(/url\(\s*(?:"([^"]*)"|'([^']*)'|([^)]*?))\s*\)/g, (match, doubleQuoted, singleQuoted, unquoted) => {
      const data = encoded.get(new URL(doubleQuoted ?? singleQuoted ?? unquoted, url).href);
      return data ? 'url("' + data + '")' : match;
    });
    encoded.set(url, await dataURL(new Blob([css], { type: 'text/css' })));
  }
  const response = resources.get(frameURL);
  const doc = new DOMParser().parseFromString(await response.clone().text(), 'text/html');
  for (const node of doc.querySelectorAll('[src], [href]')) {
    for (const attr of ['src', 'href']) {
      if (!node.hasAttribute(attr)) continue;
      const resource = new URL(node.getAttribute(attr), frameURL).href;
      const data = encoded.get(resource);
      if (data) node.setAttribute(attr, data);
      if (node.tagName === 'SCRIPT' && scripts.has(resource)) node.setAttribute('integrity', scripts.get(resource));
    }
  }
  const policy = doc.createElement('meta');
  policy.httpEquiv = 'Content-Security-Policy';
  policy.content = response.headers.get('Content-Security-Policy')
    .replace(/(?:sandbox|frame-ancestors)[^;]*;?\s*/g, '')
    .replace("script-src 'self'", 'script-src ' + [...scripts.values()].map(hash => "'" + hash + "'").join(' '))
    .replace("style-src 'self'", "style-src 'self' data:")
    .replace("font-src 'self'", "font-src 'self' data:");
  doc.head.prepend(policy);
  return URL.createObjectURL(new Blob(['<!doctype html>' + doc.documentElement.outerHTML], { type: 'text/html' }));
}

// ---- discussion ----

// threadEntries lists a thread as shown: sent messages from the server, this
// call's batch until the server holds it, then the staged draft.
function threadEntries(id) {
  const entries = serverThread(id).map(m => ({ kind: m.author === 'agent' ? 'agent' : 'user', text: m.text || '', images: (m.images || []).map(image => ({ id: image })) }));
  const batch = record.batch;
  const sent = batch && (id === OVERVIEW ? batch.overview : batch.questions[id]);
  if (sent) {
    for (const text of sent.messages || []) entries.push({ kind: 'user', text, images: [] });
    if (sent.images && sent.images.length) entries.push({ kind: 'user', text: '', images: sent.images });
  }
  const draft = peek(id);
  if (draft) {
    draft.messages.forEach((m, index) => entries.push({ kind: 'staged', text: m.text, images: [], index }));
    draft.images.forEach((image, index) => entries.push({ kind: 'staged', text: '', images: [image], image: index }));
  }
  return entries;
}

function renderDiscussion() {
  const id = active;
  const title = id === OVERVIEW ? 'Panoramica · commenti generali' : number(id) + ' · ' + question(id).title;
  setText($('#discussion-title'), title);
  composer.placeholder = id === OVERVIEW ? 'Commento generale…' : 'Approfondisci ' + label(id) + '…';
  const entries = threadEntries(id);
  setText($('#discussion-count'), plural(entries.length, 'messaggio', 'messaggi'));
  setText($('#sheet-toggle'), sheet ? 'Chiudi discussione' : 'Discussione' + (entries.length ? ' ' + entries.length : ''));
  const signature = JSON.stringify([id, entries, frozen()]);
  if (signature !== threadKey) {
    threadKey = signature;
    const list = $('#thread');
    if (!entries.length) {
      list.replaceChildren(element('li', id === OVERVIEW
        ? 'Nessun commento generale. Scrivi qui ciò che non riguarda una sola domanda.'
        : 'Nessun messaggio. Un dubbio scritto qui resta legato a ' + label(id) + '.', 'empty'));
    } else {
      list.replaceChildren(...entries.map(entry => message(id, entry)));
    }
    list.scrollTop = list.scrollHeight;
  }
  updateComposer();
}

function message(id, entry) {
  const item = element('li', undefined, 'message message-' + entry.kind);
  const who = element('p', entry.kind === 'agent' ? 'Agente' : entry.kind === 'staged' ? 'Tu · in bozza' : 'Tu', 'message-who');
  if (entry.kind === 'staged') {
    const remove = button('×', 'unstage');
    remove.setAttribute('aria-label', 'Rimuovi dalla bozza');
    remove.disabled = frozen();
    remove.addEventListener('click', () => unstage(id, entry));
    who.append(remove);
  }
  item.append(who);
  if (entry.text) item.append(element('p', entry.kind === 'agent' ? fmt(entry.text) : entry.text, 'message-text'));
  for (const image of entry.images) {
    const preview = element('img', undefined, 'message-image');
    preview.src = 'images/' + image.id;
    preview.alt = 'Screenshot';
    item.append(preview);
    if (image.bytes) item.append(element('span', 'Screenshot · ' + size(image.bytes), 'message-meta'));
  }
  return item;
}

function unstage(id, entry) {
  if (frozen()) return;
  const draft = draftOf(id);
  if (entry.index !== undefined) draft.messages.splice(entry.index, 1);
  if (entry.image !== undefined) draft.images.splice(entry.image, 1);
  imageRefusal = '';
  changed();
  renderDiscussion();
}

// The discussion opens as a sheet from the footer at phone width; elsewhere it
// stays beside the question and the toggle is hidden.
function toggleSheet() {
  sheet = !sheet;
  $('#page').toggleAttribute('data-sheet', sheet);
  $('#sheet-toggle').setAttribute('aria-expanded', String(sheet));
  renderDiscussion();
}

function updateComposer() {
  composer.readOnly = frozen();
  $('#stage-message').disabled = frozen() || !composer.value.trim();
  setText($('#composer-refusal'), imageRefusal);
}

function stageMessage() {
  const text = composer.value.trim();
  if (!text || frozen()) return;
  const draft = draftOf(active);
  draft.messages.push({ text });
  draft.composer = '';
  composer.value = '';
  setText($('#composer-status'), 'Messaggio in bozza. Parte con Send to Agent.');
  changed();
  renderDiscussion();
  composer.focus();
}

// ---- screenshots ----

function imageProblem(files) {
  const total = imageCount() + uploading + files.length;
  if (total > view.imageLimit) return `Al massimo ${view.imageLimit} screenshot per invio: con questi sarebbero ${total}. Nessuno è stato aggiunto.`;
  const large = files.find(file => file.size > view.imageBytes);
  if (large) return `«${large.name}» supera ${size(view.imageBytes)}. Nessuno screenshot è stato aggiunto.`;
  return '';
}

async function upload(file) {
  const response = await fetch('images', {
    method: 'POST',
    headers: { 'Content-Type': 'application/octet-stream', 'Lavagna-Round': view.round, 'Lavagna-Token': view.token },
    body: file,
  }).catch(() => { throw new Error('lavagna non risponde'); });
  const body = await response.json().catch(() => ({}));
  if (response.status === 415) throw new Error(`«${file.name}» non è un’immagine PNG, JPEG, WebP o GIF`);
  if (response.status !== 201) throw new Error(body.error || String(response.status));
  return { id: body.image, bytes: file.size };
}

async function attach(files) {
  if (!view || frozen()) return;
  imageRefusal = imageProblem(files);
  if (imageRefusal) {
    updateComposer();
    return;
  }
  const target = active;
  uploading += files.length;
  setText($('#composer-status'), 'Caricamento degli screenshot…');
  updateFooter();
  const added = [];
  let problem = '';
  try {
    for (const file of files) added.push(await upload(file));
  } catch (error) {
    problem = error.message + '. Nessuno screenshot è stato aggiunto.';
  } finally {
    uploading -= files.length;
  }
  if (closed) return;
  imageRefusal = problem;
  if (!problem) draftOf(target).images.push(...added);
  setText($('#composer-status'), problem ? '' : plural(added.length, 'screenshot in bozza', 'screenshot in bozza') + '. Partono con Send to Agent.');
  changed();
  renderDiscussion();
}

function carriesFiles(event) {
  return event.dataTransfer && event.dataTransfer.types.includes('Files');
}

// ---- footer and send ----

function summary(items) {
  if (!items.length) return 'Niente in bozza';
  const parts = items.map(item => item.kind === 'choice' ? label(item.id) + ' → ' + item.key
    : label(item.id) + ' +' + item.count + (item.kind === 'msg' ? ' msg' : ' screenshot'));
  return stagedCount(items) + ' in bozza · ' + parts.slice(0, 4).join(' · ') + (parts.length > 4 ? ' · +' + (parts.length - 4) : '');
}

function sendBlocker(items, over) {
  if (!live || frozen() || inactive || sentThisCall()) return true;
  return uploading > 0 || over || stagedCount(items) === 0;
}

function updateFooter() {
  renderTitle();
  if (!view) return;
  const items = staged();
  const count = stagedCount(items);
  const name = phase();
  const sentence = STAGES[name][1];
  const bytes = textBytes();
  const over = bytes > view.limit;
  let text = sentence === null ? summary(items) : sentence;
  if (sentence && draftUnwitnessed(name)) text += ' · ' + TEXT.unwitnessed;
  if (sentence && count) text += ' · ' + count + ' in bozza';
  if (refusal) text = refusal;
  const line = $('#summary');
  setText(line, text);
  line.dataset.state = refusal ? 'refused' : sentence === null ? (count ? 'draft' : '') : name;
  const counter = $('#counter');
  counter.hidden = bytes < view.limit * COUNTER_FROM;
  counter.classList.toggle('over', over);
  setText(counter, over
    ? `${kilobytes(bytes)} di ${kilobytes(view.limit)}: il testo supera il limite. Accorcia i messaggi per inviare; nulla viene tagliato.`
    : `${kilobytes(bytes)} di ${kilobytes(view.limit)} disponibili per il testo`);
  const send = $('#send');
  send.disabled = sendBlocker(items, over);
  const badge = $('#send-count');
  badge.hidden = !count;
  setText(badge, String(count));
  updateComposer();
}

function draftUnwitnessed(name) {
  const d = delivery();
  return d && d.unwitnessed && DEGRADED.includes(name);
}

function newSubmission() {
  const bytes = crypto.getRandomValues(new Uint8Array(12));
  return 's-' + Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
}

// batch collects the current answers of the current round's open questions and
// every staged message and screenshot, composer text included.
function batch() {
  const wire = { questions: {}, overview: {} };
  const shown = { questions: {}, overview: {} };
  const collect = (id, q) => {
    const entry = {};
    if (q && answerable(q)) {
      const answer = answerOf(q);
      if (answer.choice) entry.choice = answer.choice;
      else if (answer.text && answer.text.trim()) entry.answer = answer.text.trim();
    }
    const draft = peek(id);
    const messages = draft ? stagedMessages(draft) : [];
    const images = draft ? draft.images.map(image => ({ ...image })) : [];
    if (messages.length) entry.messages = messages;
    const out = { ...entry };
    if (images.length) {
      out.images = images.map(image => image.id);
      entry.images = images;
    }
    return [out, entry];
  };
  for (const q of questions()) {
    const [out, entry] = collect(q.id, q);
    if (Object.keys(out).length) {
      wire.questions[q.id] = out;
      shown.questions[q.id] = entry;
    }
  }
  [wire.overview, shown.overview] = collect(OVERVIEW, null);
  return { wire, shown };
}

function clearSent() {
  for (const id in record.questions) {
    const draft = record.questions[id];
    draft.messages = [];
    draft.images = [];
    draft.composer = '';
    delete draft.answer;
  }
  record.overview = thread();
  composer.value = '';
  setText($('#composer-status'), '');
}

async function send() {
  if (!view || $('#send').disabled) return;
  const { wire, shown } = batch();
  const submission = record.submission || newSubmission();
  record.submission = submission;
  record.delivery = { call: view.call, submission, stage: '', unwitnessed: false };
  sending = true;
  refusal = '';
  save();
  refreshViews();
  try {
    const response = await fetch('send', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ round: view.round, token: view.token, submission, ...wire }),
    });
    const body = await response.json().catch(() => ({}));
    if (closed) return;
    if (response.status === 202 || response.status === 200) {
      record.batch = { submission, ...shown };
      clearSent();
      advance({ stage: 'accepted', ...body });
      record.submission = null;
    } else if (response.status === 409) {
      inactive = true;
      refusal = body.error || String(response.status);
      record.delivery = null;
    } else {
      refusal = 'Invio rifiutato da lavagna: ' + (body.error || response.status) + '. La bozza è conservata.';
      record.delivery = null;
    }
  } catch {
    if (!closed) {
      refusal = 'Invio non riuscito: lavagna non risponde. La bozza è conservata.';
      record.delivery = null;
    }
  } finally {
    if (!closed) {
      sending = false;
      save();
      refreshViews();
    }
  }
}

// ---- view arrival ----

// ensureActive keeps the center on a question the view can show, falling back
// to the first question of the current round.
function ensureActive() {
  const q = active === OVERVIEW ? null : question(active);
  if (active === OVERVIEW || q && q.status !== 'planned') return;
  active = defaultActive();
  composer.value = draftOf(active).composer;
}

function apply(next) {
  if (closed) return;
  view = next;
  if (!record) record = stored() || blank();
  reconcile();
  ensureActive();
  $('#waiting').hidden = true;
  $('#closed').hidden = true;
  $('#page').dataset.state = 'ready';
  for (const id of ['#rail', '#discussion', '#footer']) $(id).hidden = false;
  refreshViews();
}

function adopt() {
  const next = stored();
  if (!view || !next) return;
  record = next;
  reconcile();
  ensureActive();
  const text = $('#free-text');
  const q = active === OVERVIEW ? null : question(active);
  if (text && q) {
    const draft = peek(q.id);
    text.value = draft && draft.free !== undefined ? draft.free : answerOf(q).text || '';
  }
  composer.value = draftOf(active).composer;
  refreshViews();
}

function drainWorker(worker) {
  return new Promise((resolve, reject) => {
    const channel = new MessageChannel();
    const finish = error => {
      clearTimeout(timer);
      channel.port1.close();
      if (error) reject(error); else resolve();
    };
    const timer = setTimeout(() => finish(new Error('Worker cleanup unconfirmed')), 1500);
    channel.port1.onmessage = () => finish();
    worker.postMessage({ lavagna: 'close' }, [channel.port2]);
  });
}

async function forget() {
  await Promise.allSettled([...renders]);
  let clean = true;
  try { localStorage.removeItem(RECORD_KEY); } catch { clean = false; }
  try {
    const registration = await navigator.serviceWorker.getRegistration();
    if (registration) {
      const workers = new Set([registration.installing, registration.waiting, registration.active].filter(Boolean));
      const drained = await Promise.allSettled([...workers].map(drainWorker));
      if (drained.some(result => result.status === 'rejected')) clean = false;
      await registration.unregister();
    }
    await caches.delete(CACHE_NAME);
  } catch { clean = false; }
  return clean;
}

async function showClosed(token) {
  closed = true;
  live = false;
  leaving = true;
  view = null;
  record = null;
  destroyFrame();
  centerKey = '';
  frameKey = '';
  threadKey = '';
  $('#question').hidden = true;
  $('#thread').replaceChildren();
  composer.value = '';
  for (const id of ['#rail', '#discussion', '#footer', '#waiting']) $(id).hidden = true;
  $('#closed').hidden = false;
  $('#page').dataset.state = 'closed';
  setText($('#phase-title'), '');
  setText($('#round-label'), '');
  renderTitle();
  if (await forget()) {
    try {
      await fetch(new URL('close-ack', location.href), {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token }),
      });
    } catch { }
  }
}

function connect() {
  if (closed) return;
  const source = new EventSource('events');
  source.addEventListener('round', event => {
    if (closed) return;
    live = true;
    apply(JSON.parse(event.data));
  });
  source.addEventListener('receipt', event => {
    const receipt = JSON.parse(event.data);
    const d = delivery();
    if (!d || receipt.submission !== d.submission) return;
    advance(receipt);
    save();
    updateFooter();
  });
  source.addEventListener('closed', event => {
    source.close();
    let token;
    try { token = JSON.parse(event.data).token; } catch { return; }
    showClosed(token);
  });
  source.addEventListener('error', () => {
    if (leaving) return;
    live = false;
    if (source.readyState === EventSource.CLOSED) detached = true;
    else {
      source.close();
      setTimeout(connect, RETRY_MS);
    }
    if (view) {
      refreshViews();
    } else {
      if (detached) {
        setText($('#waiting .notice-lead'), TEXT.detached);
        setText($('#waiting .muted'), 'Riapri la pagina dal link nel terminale.');
      }
      renderTitle();
    }
  });
}

// ---- events ----

composer.addEventListener('input', () => {
  if (!view) return;
  draftOf(active).composer = composer.value;
  setText($('#composer-status'), '');
  changed(false);
});

composer.addEventListener('paste', event => {
  const files = [...event.clipboardData.files];
  if (!files.length) return;
  event.preventDefault();
  attach(files);
});

$('#stage-message').addEventListener('click', stageMessage);
$('#send').addEventListener('click', send);
$('#sheet-toggle').addEventListener('click', toggleSheet);
$('#reduce').addEventListener('click', () => expand(false));
PHONE.addEventListener('change', () => {
  if (expanded) expand(true);
});

document.addEventListener('keydown', event => {
  if (event.key === 'Escape' && expanded) {
    event.preventDefault();
    expand(false);
    return;
  }
  if (event.key !== 'Enter' || !(event.metaKey || event.ctrlKey)) return;
  event.preventDefault();
  send();
});

const discussion = $('#discussion');
discussion.addEventListener('dragover', event => {
  if (!carriesFiles(event) && !event.dataTransfer.types.includes('text/uri-list')) return;
  event.preventDefault();
  event.dataTransfer.dropEffect = frozen() ? 'none' : 'copy';
  discussion.classList.toggle('dropping', !frozen());
});
discussion.addEventListener('dragleave', event => {
  if (!discussion.contains(event.relatedTarget)) discussion.classList.remove('dropping');
});
discussion.addEventListener('drop', event => {
  discussion.classList.remove('dropping');
  const files = [...event.dataTransfer.files];
  if (files.length) {
    event.preventDefault();
    attach(files);
  } else if (event.dataTransfer.types.includes('text/uri-list')) {
    event.preventDefault();
    imageRefusal = 'Link e percorsi non vengono caricati: incolla o trascina l’immagine stessa.';
    updateComposer();
  }
});
for (const type of ['dragover', 'drop']) {
  window.addEventListener(type, event => {
    if (carriesFiles(event) && !discussion.contains(event.target)) event.preventDefault();
  });
}

window.addEventListener('message', fromFrame);
window.addEventListener('pagehide', () => { leaving = true; });
window.addEventListener('pageshow', () => { leaving = closed; });
window.addEventListener('storage', event => {
  if (event.key === RECORD_KEY) adopt();
});

try { navigator.serviceWorker.register('sw.js').catch(() => { }); } catch { }
connect();
