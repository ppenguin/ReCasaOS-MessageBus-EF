package route

import (
	"crypto/ecdsa"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/IceWhaleTech/CasaOS-Common/external"
	"github.com/IceWhaleTech/CasaOS-Common/utils/jwt"
	"github.com/IceWhaleTech/CasaOS-MessageBus/codegen"
	"github.com/IceWhaleTech/CasaOS-MessageBus/config"
	"github.com/IceWhaleTech/CasaOS-MessageBus/pkg/gatewayclient"
	"github.com/IceWhaleTech/CasaOS-MessageBus/service"
	"github.com/deepmap/oapi-codegen/pkg/middleware"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/labstack/echo/v4"
	echo_middleware "github.com/labstack/echo/v4/middleware"
)

func NewAPIRouter(swagger *openapi3.T, services *service.Services) (http.Handler, error) {
	apiRoute := NewAPIRoute(services)

	e := echo.New()

	e.Use((echo_middleware.CORSWithConfig(echo_middleware.CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{echo.POST, echo.GET, echo.OPTIONS, echo.PUT, echo.DELETE},
		AllowHeaders:     []string{echo.HeaderAuthorization, echo.HeaderContentLength, echo.HeaderXCSRFToken, echo.HeaderContentType, echo.HeaderAccessControlAllowOrigin, echo.HeaderAccessControlAllowHeaders, echo.HeaderAccessControlAllowMethods, echo.HeaderConnection, echo.HeaderOrigin, echo.HeaderXRequestedWith},
		ExposeHeaders:    []string{echo.HeaderContentLength, echo.HeaderAccessControlAllowOrigin, echo.HeaderAccessControlAllowHeaders},
		MaxAge:           172800,
		AllowCredentials: true,
	})))

	e.Use(echo_middleware.Gzip())
	e.Use(echo_middleware.Recover())
	e.Use(echo_middleware.Logger())

	e.Use(echo_middleware.JWTWithConfig(echo_middleware.JWTConfig{
		Skipper: func(c echo.Context) bool {
			// skip when the request really arrived on the unix socket listener
			if fromUnixSocket(c.Request()) {
				return true
			}

			// loopback ≠ identity: in-stack callers also present the gateway service credential
			if (c.RealIP() == "::1" || c.RealIP() == "127.0.0.1") &&
				gatewayclient.ServiceAuthorizationMatches(config.CommonInfo.RuntimePath, c.Request().Header.Get(echo.HeaderAuthorization)) {
				return true
			}

			// a browser's WebSocket subscription with a one-use ticket (ticket.go)
			if isSubscriptionHandshake(c) {
				if userID, ok := subscriptionTickets.consume(c.Request()); ok {
					c.Request().Header.Set("user_id", userID)
					return true
				}
			}

			return false
		},
		ParseTokenFunc: func(token string, c echo.Context) (interface{}, error) {
			valid, claims, err := jwt.Validate(token, func() (*ecdsa.PublicKey, error) { return external.GetPublicKey(config.CommonInfo.RuntimePath) })
			if err != nil || !valid {
				return nil, echo.ErrUnauthorized
			}

			c.Request().Header.Set("user_id", strconv.Itoa(claims.ID))

			return claims, nil
		},
		TokenLookupFuncs: []echo_middleware.ValuesExtractor{
			func(c echo.Context) ([]string, error) {
				return []string{authorizationToken(c.Request().Header.Get(echo.HeaderAuthorization))}, nil
			},
		},
	}))

	e.Use(middleware.OapiRequestValidatorWithOptions(swagger, &middleware.Options{Options: openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}))

	apiPath, err := getAPIPath(getSwaggerURL(swagger))
	if err != nil {
		return nil, err
	}

	codegen.RegisterHandlersWithBaseURL(e, apiRoute, apiPath)

	return e, nil
}

// fromUnixSocket: request arrived on the unix listener (root-only, main.go).
// From the connection, not the Host header (client-chosen, forwarded by the gateway).
func fromUnixSocket(r *http.Request) bool {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	return ok && addr.Network() == "unix"
}

func NewDocRouter(swagger *openapi3.T, docHTML string, docYAML string) (http.Handler, error) {
	apiPath, err := getAPIPath(getSwaggerURL(swagger))
	if err != nil {
		return nil, err
	}

	docPath := "/doc" + apiPath

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == docPath {
			if _, err := w.Write([]byte(docHTML)); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}

		if r.URL.Path == docPath+"/openapi.yaml" {
			if _, err := w.Write([]byte(docYAML)); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}
	}), nil
}

func getSwaggerURL(swagger *openapi3.T) string {
	return swagger.Servers[0].URL
}

func getAPIPath(swaggerURL string) (string, error) {
	u, err := url.Parse(swaggerURL)
	if err != nil {
		return "", err
	}

	return strings.TrimRight(u.Path, "/"), nil
}
