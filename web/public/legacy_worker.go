package public

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// legacyWorkerKillSwitch replaces the service worker that fork releases
// before 3.0 registered at /komari-sw.js. Browsers still controlled by it
// would keep serving the old cached admin app, which calls APIs that no
// longer exist. Serving this script lets the browser's update check install
// it; it then drops every cache, unregisters itself and reloads open tabs.
const legacyWorkerKillSwitch = `self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    const keys = await caches.keys();
    await Promise.all(keys.map((key) => caches.delete(key)));
    await self.registration.unregister();
    const clients = await self.clients.matchAll({ type: 'window' });
    for (const client of clients) client.navigate(client.url);
  })());
});
`

func registerLegacyWorkerKillSwitch(r *gin.RouterGroup) {
	r.GET("/komari-sw.js", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Service-Worker-Allowed", "/")
		c.Data(http.StatusOK, "text/javascript; charset=utf-8", []byte(legacyWorkerKillSwitch))
	})
}
