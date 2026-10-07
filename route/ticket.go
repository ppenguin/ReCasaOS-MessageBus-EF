package route

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

// Subscription tickets (browser WebSockets: no Authorization header; no tokens in URLs)
// - POST /ticket with normal Authorization → cookie: random, 1 handshake, 30 s,
//   user + User-Agent bound, HttpOnly, SameSite=Strict, Path=/v2/message_bus/
// - handshake redeems it; same pattern as the root service's SSH terminal ticket

const (
	subscriptionTicketCookie      = "recasaos_bus_ticket"
	subscriptionTicketLifetime    = 30 * time.Second
	maxPendingSubscriptionTickets = 64
)

var errTooManySubscriptionTickets = errors.New("too many pending subscription tickets")

// subscriptionRoutes: handshakes a ticket opens (router paths; c.Path() set before middleware).
// not socket.io polling
var subscriptionRoutes = map[string]bool{
	"/v2/message_bus/event/:source_id":  true,
	"/v2/message_bus/action/:source_id": true,
	"/v2/message_bus/socket.io":         true,
	"/v2/message_bus/socket.io/":        true,
}

func isSubscriptionHandshake(c echo.Context) bool {
	r := c.Request()
	return r.Method == http.MethodGet && strings.EqualFold(r.Header.Get(echo.HeaderUpgrade), "websocket") && subscriptionRoutes[c.Path()]
}

type subscriptionTicket struct {
	userID    string
	userAgent string
	expires   time.Time
}

type subscriptionTicketRegistry struct {
	mu      sync.Mutex
	tickets map[string]subscriptionTicket
	now     func() time.Time
}

var subscriptionTickets = newSubscriptionTicketRegistry()

func newSubscriptionTicketRegistry() *subscriptionTicketRegistry {
	return &subscriptionTicketRegistry{tickets: map[string]subscriptionTicket{}, now: time.Now}
}

func (r *subscriptionTicketRegistry) issue(userID, userAgent string) (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	value := base64.RawURLEncoding.EncodeToString(random)
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked(now)
	if len(r.tickets) >= maxPendingSubscriptionTickets {
		return "", errTooManySubscriptionTickets
	}
	r.tickets[value] = subscriptionTicket{userID: userID, userAgent: userAgent, expires: now.Add(subscriptionTicketLifetime)}
	return value, nil
}

// consume redeems the request's ticket cookie once and returns its user.
func (r *subscriptionTicketRegistry) consume(request *http.Request) (string, bool) {
	value, count := "", 0
	for _, cookie := range request.Cookies() {
		if cookie.Name == subscriptionTicketCookie {
			value = cookie.Value
			count++
		}
	}
	if count != 1 || len(value) != 43 {
		return "", false
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked(now)
	ticket, exists := r.tickets[value]
	delete(r.tickets, value)
	if !exists || !now.Before(ticket.expires) || ticket.userAgent != request.UserAgent() {
		return "", false
	}
	return ticket.userID, true
}

func (r *subscriptionTicketRegistry) cleanupLocked(now time.Time) {
	for value, ticket := range r.tickets {
		if !now.Before(ticket.expires) {
			delete(r.tickets, value)
		}
	}
}

// IssueSubscriptionTicket: POST /ticket, behind JWT; users only (not in-stack services)
func (r *APIRoute) IssueSubscriptionTicket(ctx echo.Context) error {
	userID := ctx.Request().Header.Get("user_id")
	if userID == "" {
		message := "subscription tickets are for signed-in users"
		return ctx.JSON(http.StatusForbidden, map[string]*string{"message": &message})
	}
	value, err := subscriptionTickets.issue(userID, ctx.Request().UserAgent())
	if err != nil {
		message := err.Error()
		return ctx.JSON(http.StatusTooManyRequests, map[string]*string{"message": &message})
	}
	ctx.SetCookie(&http.Cookie{
		Name:     subscriptionTicketCookie,
		Value:    value,
		Path:     "/v2/message_bus/",
		MaxAge:   int(subscriptionTicketLifetime.Seconds()),
		HttpOnly: true,
		Secure:   secureTicketCookie(ctx.Request()),
		SameSite: http.SameSiteStrictMode,
	})
	return ctx.NoContent(http.StatusNoContent)
}

// secureTicketCookie: HTTPS directly, or per Origin (behind gateway / TLS proxy)
func secureTicketCookie(request *http.Request) bool {
	return request.TLS != nil || strings.HasPrefix(strings.ToLower(request.Header.Get("Origin")), "https://")
}
