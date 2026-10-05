'use strict';

const TEXT = {
  accepted: 'Ricevuto da lavagna · non ancora consegnato all’agente',
  returned: 'Consegnato al terminale',
  detached: 'Questa scheda non è più collegata alla conversazione',
};
const STAGES = ['', 'accepted', 'returned'];
const DRAFT_PREFIX = 'lavagna:draft:';

const $ = selector => document.querySelector(selector);
const form = $('#feedback-form');
const editor = $('#comment-text');
const encoder = new TextEncoder();

let view = null;
let draft = null;
let sending = false;
let live = false;

function storageKey() { return DRAFT_PREFIX + view.token; }

function load() {
  try {
    const stored = JSON.parse(localStorage.getItem(storageKey()));
    if (stored) return stored;
  } catch { }
  return { choices: {}, comments: [], editor: '', editing: null, previous: null, next: 0, submission: null, stage: '' };
}

function save() {
  if (!view || !draft) return;
  if (draft.stage === 'returned') {
    forget();
    return;
  }
  try { localStorage.setItem(storageKey(), JSON.stringify(draft)); } catch { }
}

function forget() {
  try { localStorage.removeItem(storageKey()); } catch { }
}

function forgetAll() {
  try {
    for (const key of Object.keys(localStorage)) {
      if (key.startsWith(DRAFT_PREFIX)) localStorage.removeItem(key);
    }
  } catch { }
}

function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  if (className) node.className = className;
  return node;
}

function collectComments() {
  const result = draft.comments.map(comment => ({ ...comment }));
  const text = editor.value.trim();
  if (draft.editing !== null) {
    const index = result.findIndex(comment => comment.id === draft.editing);
    if (index !== -1 && text) result[index] = { ...result[index], text };
  } else if (text) {
    result.push({ id: 'draft', text });
  }
  return result;
}

const LINE_SEPARATORS = new RegExp('[' + String.fromCharCode(0x2028, 0x2029) + ']', 'g');

function encodedBytes(text) {
  const separators = (text.match(LINE_SEPARATORS) || []).length;
  return encoder.encode(JSON.stringify(text)).length - 2 + 3 * separators;
}

function commentBytes() {
  return collectComments().reduce((total, comment) => total + encodedBytes(comment.text), 0);
}

function sent() { return draft.stage !== ''; }

function frozen() { return sent() || sending; }

function advance(stage) {
  if (STAGES.indexOf(stage) > STAGES.indexOf(draft.stage)) draft.stage = stage;
}

function updateControls() {
  if (!view) return;
  const text = editor.value.trim();
  const locked = frozen();
  editor.readOnly = locked;
  for (const input of document.querySelectorAll('#document input[type=radio]')) input.disabled = locked;
  $('#add-comment').disabled = locked || !text;
  $('#add-comment').textContent = draft.editing === null ? 'Aggiungi commento' : 'Salva modifica';
  $('#cancel-edit').hidden = draft.editing === null;

  const bytes = commentBytes();
  const over = bytes > view.limit;
  const counter = $('#comment-counter');
  counter.classList.toggle('over', over);
  counter.textContent = over
    ? `${bytes} di ${view.limit} byte: il testo supera il limite. Accorcia i commenti per inviare; nulla viene tagliato.`
    : `${bytes} di ${view.limit} byte`;

  const count = collectComments().length;
  const answers = Object.keys(draft.choices).length;
  const summary = [];
  if (answers) summary.push(answers + (answers === 1 ? ' risposta' : ' risposte'));
  if (count) summary.push(count + (count === 1 ? ' commento' : ' commenti'));
  $('#send-summary').textContent = sent() ? 'Feedback inviato per questo round.'
    : sending ? 'Invio in corso…'
    : draft.editing !== null ? 'Salva o annulla la modifica prima di inviare.'
    : summary.length ? summary.join(' · ') + ' nel prossimo invio' : 'Nessuna scelta o commento preparato.';
  $('#send-feedback').disabled = !live || locked || over || draft.editing !== null || (!answers && count === 0);
}

function setDelivery(text, refused) {
  const delivery = $('#delivery');
  delivery.textContent = text;
  delivery.classList.toggle('refused', Boolean(refused));
}

function showStage() {
  setDelivery(draft.stage ? TEXT[draft.stage] : '');
}

function renderComments() {
  const list = $('#comments');
  list.replaceChildren();
  for (const comment of draft.comments) {
    const item = element('li', undefined, 'note' + (comment.id === draft.editing ? ' editing' : ''));
    item.append(element('div', 'Sulla pagina nel suo insieme', 'ref'));
    item.append(element('p', comment.id === draft.editing ? 'Stai modificando questo commento nell’editor.' : comment.text));
    const actions = element('div', undefined, 'note-actions');
    const edit = element('button', 'Modifica');
    edit.type = 'button';
    edit.disabled = draft.editing !== null || frozen();
    edit.addEventListener('click', () => {
      draft.previous = editor.value;
      draft.editing = comment.id;
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
  if (!sent()) draft.submission = null;
  save();
  updateControls();
}

function restorePrevious() {
  editor.value = draft.previous || '';
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
    const item = element('li');
    item.append(link);
    route.append(item);
  }
}

function render(next) {
  view = next;
  draft = load();
  sending = false;
  $('#document').innerHTML = view.html;
  renderRoute();
  $('#round-label').textContent = 'Round ' + view.round.replace(/^r/, '');
  document.title = 'lavagna · round ' + view.round.replace(/^r/, '');
  for (const input of document.querySelectorAll('#document input[type=radio]')) {
    input.checked = draft.choices[input.dataset.question] === input.value;
    input.addEventListener('change', () => {
      draft.choices[input.dataset.question] = input.value;
      changed();
    });
  }
  editor.value = draft.editor;
  $('#editor-status').textContent = '';
  $('#waiting').hidden = true;
  $('#closed').hidden = true;
  form.hidden = false;
  renderComments();
  showStage();
  updateControls();
}

function newSubmission() {
  const bytes = crypto.getRandomValues(new Uint8Array(12));
  return 's-' + Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
}

async function send() {
  if (!view || $('#send-feedback').disabled) return;
  draft.submission = draft.submission || newSubmission();
  const comments = collectComments().map(comment => ({ text: comment.text }));
  const batch = { round: view.round, token: view.token, submission: draft.submission, choices: draft.choices, comments };
  sending = true;
  save();
  renderComments();
  updateControls();
  try {
    const response = await fetch('send', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(batch),
    });
    const body = await response.json().catch(() => ({}));
    if (response.status === 202 || response.status === 200) {
      advance(body.stage || 'accepted');
      draft.comments = comments.map((comment, index) => ({ id: index + 1, text: comment.text }));
      draft.editing = null;
      editor.value = '';
      draft.editor = '';
      renderComments();
      showStage();
    } else if (response.status === 409) {
      setDelivery(body.error || String(response.status), true);
    } else {
      setDelivery('Invio rifiutato da lavagna: ' + (body.error || response.status) + '. La bozza è conservata.', true);
    }
  } catch {
    setDelivery('Invio non riuscito: lavagna non risponde. La bozza è conservata.', true);
  } finally {
    sending = false;
    save();
    renderComments();
    updateControls();
  }
}

editor.addEventListener('input', () => {
  $('#editor-status').textContent = '';
  changed();
});

$('#add-comment').addEventListener('click', () => {
  const text = editor.value.trim();
  if (!text) return;
  if (draft.editing !== null) {
    const comment = draft.comments.find(item => item.id === draft.editing);
    if (comment) comment.text = text;
    restorePrevious();
  } else {
    draft.comments.push({ id: ++draft.next, text });
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

form.addEventListener('submit', event => {
  event.preventDefault();
  send();
});

function connect() {
  const connection = $('#connection');
  const source = new EventSource('events');
  source.addEventListener('round', event => {
    const next = JSON.parse(event.data);
    live = true;
    connection.textContent = 'Collegato a lavagna';
    connection.classList.remove('lost');
    if (!view || view.token !== next.token) render(next);
    else updateControls();
  });
  source.addEventListener('receipt', event => {
    const receipt = JSON.parse(event.data);
    if (!view || !draft || receipt.submission !== draft.submission) return;
    advance(receipt.stage);
    save();
    showStage();
    updateControls();
  });
  source.addEventListener('closed', () => {
    source.close();
    live = false;
    forgetAll();
    view = null;
    form.hidden = true;
    $('#waiting').hidden = true;
    $('#closed').hidden = false;
    $('#route').replaceChildren();
    $('#round-label').textContent = '';
    connection.textContent = '';
  });
  source.addEventListener('error', () => {
    live = false;
    if (source.readyState === EventSource.CLOSED) {
      connection.textContent = TEXT.detached;
      connection.classList.add('lost');
    } else {
      connection.textContent = draft && draft.stage === 'returned' ? 'In attesa del prossimo round' : 'Riconnessione a lavagna…';
    }
    updateControls();
  });
}

connect();
