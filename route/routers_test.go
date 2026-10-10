package route

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IceWhaleTech/CasaOS-MessageBus/codegen"
	"github.com/IceWhaleTech/CasaOS-MessageBus/repository"
	"github.com/IceWhaleTech/CasaOS-MessageBus/service"
	"gotest.tools/assert"
)

func newTestAPIRouter(t *testing.T) http.Handler {
	t.Helper()

	swagger, err := codegen.GetSwagger()
	assert.NilError(t, err)

	repository, err := repository.NewDatabaseRepositoryInMemory()
	assert.NilError(t, err)
	t.Cleanup(func() { repository.Close() })

	services := service.NewServices(&repository)

	router, err := NewAPIRouter(swagger, &services)
	assert.NilError(t, err)

	return router
}

// unix-socket identity: listener, not Host header
func TestAPIRouterUnixSocketIdentity(t *testing.T) {
	router := newTestAPIRouter(t)

	listEventTypes := func(host string, local net.Addr) int {
		req := httptest.NewRequest(http.MethodGet, "/v2/message_bus/event_type", nil)
		req.RemoteAddr = "192.0.2.10:40000" // a LAN peer, not loopback
		req.Host = host
		if local != nil {
			req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, local))
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	tcp := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
	unix := &net.UnixAddr{Name: "/tmp/message-bus.sock", Net: "unix"}

	assert.Equal(t, listEventTypes("unix", nil), http.StatusUnauthorized, "Host: unix without a connection")
	assert.Equal(t, listEventTypes("unix", tcp), http.StatusUnauthorized, "Host: unix over TCP")
	assert.Equal(t, listEventTypes("example.org", unix), http.StatusOK, "the unix socket listener")
	assert.Equal(t, listEventTypes("unix", unix), http.StatusOK, "the in-tree socket client")
}
