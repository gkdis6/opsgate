'use strict';
self.addEventListener('push', event => {
  event.waitUntil(self.registration.showNotification('opsgate', {
    body: NOTIFICATION_BODY,
    tag: 'opsgate-approval'
  }));
});
self.addEventListener('notificationclick', event => {
  event.notification.close();
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({type: 'window', includeUncontrolled: true});
    for (const client of windows) {
      const url = new URL(client.url);
      if (url.origin === self.location.origin && url.pathname === '/approvals') {
        await client.navigate('/approvals');
        return client.focus();
      }
    }
    return self.clients.openWindow('/approvals');
  })());
});
