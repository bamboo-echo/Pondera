/* Pondera Mobile service worker.
 * - Offline shell for the phone UI and settings page
 * - Web Push notification rendering
 * - Background Sync drain of the offline command outbox (IndexedDB)
 * No API responses are cached: a stale task view must never look live.
 */
const CACHE = 'pondera-mobile-v2';
const SHELL = ['/mobile', '/mobile/manifest.webmanifest', '/settings'];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE).then((c) => c.addAll(SHELL).catch(() => undefined)).then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) => Promise.all(
      keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))
    )).then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  // Never cache API/SSE traffic.
  if (url.pathname.startsWith('/events') || url.pathname.startsWith('/mirror') ||
      url.pathname.startsWith('/cost') || url.pathname.startsWith('/cloud') ||
      url.pathname.startsWith('/push') || url.pathname.startsWith('/status') ||
      url.pathname.startsWith('/message') || url.pathname.startsWith('/history')) return;
  if (req.mode === 'navigate' || SHELL.includes(url.pathname)) {
    event.respondWith(
      fetch(req).then((res) => {
        const copy = res.clone();
        caches.open(CACHE).then((c) => c.put(req, copy)).catch(() => {});
        return res;
      }).catch(() => caches.match(req).then((hit) => hit || caches.match('/mobile')))
    );
  }
});

self.addEventListener('push', (event) => {
  let data = {};
  try { data = event.data ? event.data.json() : {}; } catch (e) {
    data = { title: 'Pondera', body: event.data ? event.data.text() : '' };
  }
  const title = data.title || 'Pondera';
  const options = {
    body: data.body || '',
    tag: data.tag || 'pondera-task',
    renotify: true,
    icon: '/mobile/manifest.webmanifest',
    badge: '/mobile/manifest.webmanifest',
    data: { url: data.url || '/mobile' },
    actions: [
      { action: 'open', title: '查看任务' },
      { action: 'dismiss', title: '忽略' }
    ]
  };
  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  if (event.action === 'dismiss') return;
  const target = (event.notification.data && event.notification.data.url) || '/mobile';
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((list) => {
      for (const client of list) {
        if ('focus' in client) {
          client.navigate(target).catch(() => {});
          return client.focus();
        }
      }
      return self.clients.openWindow(target);
    })
  );
});

/* ---- offline command outbox (shared with the page via IndexedDB) ---- */
function openOutbox() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open('pondera', 1);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains('outbox')) {
        db.createObjectStore('outbox', { keyPath: 'id' });
      }
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

function readOutbox(db) {
  return new Promise((resolve, reject) => {
    const tx = db.transaction('outbox', 'readonly');
    const req = tx.objectStore('outbox').getAll();
    req.onsuccess = () => resolve(req.result || []);
    req.onerror = () => reject(req.error);
  });
}

function deleteOutbox(db, id) {
  return new Promise((resolve, reject) => {
    const tx = db.transaction('outbox', 'readwrite');
    tx.objectStore('outbox').delete(id);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

async function flushOutbox() {
  let db;
  try { db = await openOutbox(); } catch (e) { return; }
  const items = await readOutbox(db).catch(() => []);
  for (const item of items) {
    try {
      const res = await fetch(item.url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(item.body)
      });
      // 2xx means delivered; 409/429 are retried on the next sync.
      if (res.ok || res.status === 409) {
        await deleteOutbox(db, item.id);
      }
    } catch (e) {
      // Still offline: leave the item queued.
      break;
    }
  }
}

self.addEventListener('sync', (event) => {
  if (event.tag === 'pondera-outbox') event.waitUntil(flushOutbox());
});

self.addEventListener('message', (event) => {
  if (event.data && event.data.type === 'FLUSH_OUTBOX') {
    event.waitUntil(flushOutbox());
  }
});
