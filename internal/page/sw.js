'use strict';

const SCOPE = self.registration.scope;
const CACHE = 'lavagna:' + new URL(SCOPE).pathname;
const SHELL = ['', 'assets/lavagna.css', 'assets/shell.js', 'assets/fonts/AtkinsonHyperlegibleNext.ttf']
  .map(path => new URL(path, SCOPE).href);
const pending = new Set();
let closed = false;

function track(task) {
  pending.add(task);
  task.then(() => pending.delete(task), () => pending.delete(task));
  return task;
}

self.addEventListener('install', event => {
  event.waitUntil(track(caches.open(CACHE).then(cache => cache.addAll(SHELL)).then(() => self.skipWaiting())));
});

self.addEventListener('activate', event => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('message', event => {
  if (event.data?.lavagna !== 'close' || !event.source?.url.startsWith(SCOPE) || !event.ports[0]) return;
  closed = true;
  event.waitUntil(Promise.allSettled([...pending]).then(() => event.ports[0].postMessage('drained')));
});

self.addEventListener('fetch', event => {
  const url = new URL(event.request.url);
  url.search = '';
  url.hash = '';
  if (closed || event.request.method !== 'GET' || !(SHELL.includes(url.href) || url.href.startsWith(new URL('images/', SCOPE).href))) return;
  event.respondWith(track(fetch(event.request).then(async response => {
    if (response.ok && !closed) {
      const cache = await caches.open(CACHE);
      await cache.put(url.href, response.clone());
    }
    return response;
  }, () => caches.match(url.href).then(hit => hit || Response.error()))));
});
