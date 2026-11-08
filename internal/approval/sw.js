'use strict';
self.addEventListener('push', event => {
  event.waitUntil(self.registration.showNotification('opsgate', {
    body: NOTIFICATION_BODY,
    tag: 'opsgate-approval'
  }));
});
self.addEventListener('notificationclick', event => {
  event.notification.close();
  // The push carries no content; the server picks the newest waiting item.
  const target = '/approvals/next';
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({type: 'window', includeUncontrolled: true});
    for (const client of windows) {
      const url = new URL(client.url);
      // Only pages this worker controls (under /approvals) can be navigated.
      if (url.origin === self.location.origin && url.pathname.startsWith('/approvals') && 'navigate' in client) {
        await client.focus();
        return client.navigate(target);
      }
    }
    return self.clients.openWindow(target);
  })());
});
