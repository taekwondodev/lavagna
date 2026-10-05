'use strict';

const SCOPE = self.registration.scope;
const CACHE = 'lavagna:' + new URL(SCOPE).pathname;
const SHELL = ['', 'assets/lavagna.css', 'assets/shell.js', 'assets/fonts/AtkinsonHyperlegibleNext.ttf']
  .map(path => new URL(path, SCOPE).href);

self.addEventListener('install', event => {
  event.waitUntil(caches.open(CACHE).then(cache => cache.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', event => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('fetch', event => {
  const url = new URL(event.request.url);
  url.search = '';
  url.hash = '';
  if (event.request.method !== 'GET' || !(SHELL.includes(url.href) || url.href.startsWith(new URL('images/', SCOPE).href))) return;
  event.respondWith(fetch(event.request).then(response => {
    if (response.ok) {
      const copy = response.clone();
      event.waitUntil(caches.open(CACHE).then(cache => cache.put(url.href, copy)));
    }
    return response;
  }, () => caches.match(url.href).then(hit => hit || Response.error())));
});
