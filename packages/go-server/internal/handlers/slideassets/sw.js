// Slide remote shell. Precaches the page, manifest, and icons only.
// A controlled page's /api fetches still hit this worker; they return
// before any cache read or write so a tap is never served from cache.
var CACHE = 'slide-shell-v1';
var SHELL = [
  '/slide',
  '/slide/manifest.webmanifest',
  '/slide/icon-180.png',
  '/slide/icon-192.png',
  '/slide/icon-512.png'
];

function isAPI(pathname) {
  return pathname === '/api' || pathname.indexOf('/api/') === 0;
}

function isShell(pathname) {
  for (var i = 0; i < SHELL.length; i++) {
    if (SHELL[i] === pathname) return true;
  }
  return false;
}

self.addEventListener('install', function (event) {
  event.waitUntil(caches.open(CACHE).then(function (cache) {
    return cache.addAll(SHELL);
  }));
});

self.addEventListener('activate', function (event) {
  event.waitUntil(caches.keys().then(function (keys) {
    return Promise.all(keys.map(function (key) {
      if (key !== CACHE) return caches.delete(key);
    }));
  }).then(function () {
    return self.clients.claim();
  }));
});

self.addEventListener('fetch', function (event) {
  var url = new URL(event.request.url);
  if (url.origin !== self.location.origin) return;
  // Never cache API calls. Leaving the event untouched keeps them on the network.
  if (isAPI(url.pathname)) return;
  if (event.request.method !== 'GET' || !isShell(url.pathname)) return;
  event.respondWith(fetch(event.request).catch(function () {
    return caches.match(event.request);
  }));
});
