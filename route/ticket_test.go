package route

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IceWhaleTech/CasaOS-Common/external"
	"github.com/IceWhaleTech/CasaOS-Common/utils/jwt"
	"github.com/IceWhaleTech/CasaOS-MessageBus/config"
	"gotest.tools/assert"
)

func ticketRequest(cookie, userAgent string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/v2/message_bus/socket.io/", nil)
	request.Header.Set("User-Agent", userAgent)
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: subscriptionTicketCookie, Value: cookie})
	}
	return request
}

func TestSubscriptionTicketIsOneUseAndBound(t *testing.T) {
	registry := newSubscriptionTicketRegistry()
	value, err := registry.issue("7", "browser")
	assert.NilError(t, err)

	_, ok := registry.consume(ticketRequest(value, "another browser"))
	assert.Assert(t, !ok, "a ticket from another User-Agent")
	_, ok = registry.consume(ticketRequest(value, "browser"))
	assert.Assert(t, !ok, "a ticket is gone after any redemption attempt")

	value, err = registry.issue("7", "browser")
	assert.NilError(t, err)
	userID, ok := registry.consume(ticketRequest(value, "browser"))
	assert.Assert(t, ok)
	assert.Equal(t, userID, "7")
	_, ok = registry.consume(ticketRequest(value, "browser"))
	assert.Assert(t, !ok, "a replayed ticket")

	_, ok = registry.consume(ticketRequest("", "browser"))
	assert.Assert(t, !ok, "no ticket")
	_, ok = registry.consume(ticketRequest("not-a-ticket", "browser"))
	assert.Assert(t, !ok, "a made-up ticket")
}

func TestSubscriptionTicketExpires(t *testing.T) {
	registry := newSubscriptionTicketRegistry()
	now := time.Now()
	registry.now = func() time.Time { return now }
	value, err := registry.issue("7", "browser")
	assert.NilError(t, err)
	now = now.Add(subscriptionTicketLifetime)
	_, ok := registry.consume(ticketRequest(value, "browser"))
	assert.Assert(t, !ok, "an expired ticket")
}

func TestSubscriptionTicketsAreBounded(t *testing.T) {
	registry := newSubscriptionTicketRegistry()
	for i := 0; i < maxPendingSubscriptionTickets; i++ {
		_, err := registry.issue("7", "browser")
		assert.NilError(t, err)
	}
	_, err := registry.issue("7", "browser")
	assert.Equal(t, err, errTooManySubscriptionTickets)
}

// newTicketTestRouter: real router + user service serving JWKS + access token for user 7
// one key pair for all tests: CasaOS-Common caches the public key process-wide
var ticketTestKey, _, _ = jwt.GenerateKeyPair()

func newTicketTestRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	privateKey := ticketTestKey
	jwks, err := jwt.GenerateJwksJSON(&privateKey.PublicKey)
	assert.NilError(t, err)
	userService := httptest.NewServer(jwt.JWKSHandler(jwks))
	t.Cleanup(userService.Close)

	runtimePath := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(runtimePath, external.UserServiceAddressFilename), []byte(userService.URL), 0o600))
	previous := config.CommonInfo.RuntimePath
	config.CommonInfo.RuntimePath = runtimePath
	t.Cleanup(func() { config.CommonInfo.RuntimePath = previous })

	token, err := jwt.GetAccessToken("admin", privateKey, 7)
	assert.NilError(t, err)
	return newTestAPIRouter(t), token
}

func serve(router http.Handler, request *http.Request) *httptest.ResponseRecorder {
	request.RemoteAddr = "192.0.2.10:40000" // through the gateway: a LAN client
	request.Header.Set("User-Agent", "browser")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func handshake(cookie *http.Cookie) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/v2/message_bus/socket.io/?EIO=3&transport=websocket", nil)
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Connection", "Upgrade")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	return request
}

func TestSubscriptionTicketThroughTheRouter(t *testing.T) {
	router, token := newTicketTestRouter(t)
	subscriptionTickets = newSubscriptionTicketRegistry()

	// no ticket without a user token
	assert.Equal(t, serve(router, httptest.NewRequest(http.MethodPost, "/v2/message_bus/ticket", nil)).Code, http.StatusUnauthorized)

	request := httptest.NewRequest(http.MethodPost, "/v2/message_bus/ticket", nil)
	request.Header.Set("Authorization", token)
	response := serve(router, request)
	assert.Equal(t, response.Code, http.StatusNoContent, response.Body.String())
	cookies := response.Result().Cookies()
	assert.Equal(t, len(cookies), 1)
	cookie := cookies[0]
	assert.Equal(t, cookie.Name, subscriptionTicketCookie)
	assert.Assert(t, cookie.HttpOnly)
	assert.Equal(t, cookie.SameSite, http.SameSiteStrictMode)
	assert.Equal(t, cookie.Path, "/v2/message_bus/")
	assert.Equal(t, cookie.MaxAge, 30)

	// the ticket opens nothing but a subscription handshake
	other := httptest.NewRequest(http.MethodGet, "/v2/message_bus/event_type", nil)
	other.AddCookie(cookie)
	assert.Equal(t, serve(router, other).Code, http.StatusUnauthorized)

	// redeemed by the handshake, once
	serve(router, handshake(&http.Cookie{Name: cookie.Name, Value: cookie.Value}))
	_, ok := subscriptionTickets.consume(ticketRequest(cookie.Value, "browser"))
	assert.Assert(t, !ok, "the handshake did not redeem the ticket")
}

// no ticket / user token / in-stack credential → handshake refused
func TestSubscriptionHandshakeNeedsATicket(t *testing.T) {
	router, token := newTicketTestRouter(t)
	subscriptionTickets = newSubscriptionTicketRegistry()

	assert.Equal(t, serve(router, handshake(nil)).Code, http.StatusUnauthorized)
	assert.Equal(t, serve(router, handshake(&http.Cookie{Name: subscriptionTicketCookie, Value: "forged"})).Code, http.StatusUnauthorized)

	request := httptest.NewRequest(http.MethodPost, "/v2/message_bus/ticket", nil)
	request.Header.Set("Authorization", token)
	cookie := serve(router, request).Result().Cookies()[0]
	ticket := &http.Cookie{Name: cookie.Name, Value: cookie.Value}
	assert.Assert(t, serve(router, handshake(ticket)).Code != http.StatusUnauthorized, "a ticketed handshake was refused")
	assert.Equal(t, serve(router, handshake(ticket)).Code, http.StatusUnauthorized, "a replayed ticket")
}
