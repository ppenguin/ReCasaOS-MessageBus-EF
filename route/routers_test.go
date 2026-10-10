package route

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/IceWhaleTech/CasaOS-MessageBus/codegen"
	"github.com/IceWhaleTech/CasaOS-MessageBus/config"
	"github.com/IceWhaleTech/CasaOS-MessageBus/pkg/gatewayclient"
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

// loopback skips JWT only with the gateway service credential
func TestAPIRouterLoopbackNeedsServiceCredential(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	runtimePath := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(runtimePath, gatewayclient.ServiceTokenFilename), []byte(token+"\n"), 0o600))
	previous := config.CommonInfo.RuntimePath
	config.CommonInfo.RuntimePath = runtimePath
	t.Cleanup(func() { config.CommonInfo.RuntimePath = previous })

	router := newTestAPIRouter(t)

	listEventTypes := func(remote, authorization string) int {
		req := httptest.NewRequest(http.MethodGet, "/v2/message_bus/event_type", nil)
		req.RemoteAddr = remote
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, listEventTypes("127.0.0.1:40000", ""), http.StatusUnauthorized, "loopback without a credential")
	assert.Equal(t, listEventTypes("[::1]:40000", ""), http.StatusUnauthorized, "IPv6 loopback without a credential")
	assert.Equal(t, listEventTypes("127.0.0.1:40000", "Bearer "+token[:len(token)-1]+"0"), http.StatusUnauthorized, "loopback with a wrong credential")
	assert.Equal(t, listEventTypes("192.0.2.10:40000", "Bearer "+token), http.StatusUnauthorized, "the credential from the LAN")
	assert.Equal(t, listEventTypes("127.0.0.1:40000", "Bearer "+token), http.StatusOK, "loopback with the credential")
	assert.Equal(t, listEventTypes("[::1]:40000", token), http.StatusOK, "IPv6 loopback with the bare credential")
}
