package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mmt-delivery/db"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/jwk"
	"go.uber.org/zap"
)

// Middleware authenticates API requests via two mechanisms in priority order:
//
//  1. Authorization: Bearer <token> — validated using the JKU/KID embedded in the
//     JWT header, fetching the public key from the issuer's JWKS endpoint.
//  2. Session cookie — the access token stored in the session is decoded (without
//     re-verifying the signature, since it was obtained from the issuer during login)
//     and used to populate the same context keys.
//
// If neither mechanism provides a valid identity the request is aborted with 401.
func Middleware(sessions *SessionStore, logger *zap.SugaredLogger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. Try Bearer token first.
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			token, err := jwt.ParseWithClaims(tokenStr, &db.UaaClaims{}, func(t *jwt.Token) (any, error) {
				jku, _ := t.Header["jku"].(string)
				kid, _ := t.Header["kid"].(string)
				if jku == "" || kid == "" {
					return nil, fmt.Errorf("missing jku or kid in token header")
				}
				return keyFromJKU(jku, kid)
			})
			if err != nil || !token.Valid {
				msg := "invalid token"
				if err != nil {
					msg = "invalid token: " + err.Error()
				}
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": msg})
				return
			}
			claims := token.Claims.(*db.UaaClaims)

			if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "token has expired"})
				return
			}

			setClaims(c, claims, tokenStr)
			c.Next()
			return
		}

		// 2. Try session cookie.
		cookieVal, err := c.Cookie(sessions.CookieName())
		if err == nil && cookieVal != "" {
			if data, ok := sessions.Get(cookieVal); ok {
				claims, err := parseClaimsFromToken(data.AccessToken)
				if err != nil {
					logger.Warnw("failed to parse claims from session token", "error", err)
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "invalid session token"})
					return
				}
				setClaims(c, claims, data.AccessToken)
				c.Next()
				return
			}
		}

		// 3. No valid auth.
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "authentication required"})
	}
}

// SessionMiddleware authenticates browser-facing routes that require a session.
// Unlike Middleware it does not accept Bearer tokens — it is intended for HTML
// routes where an unauthenticated user should be redirected to the OAuth login
// page rather than receiving a JSON error.
//
// If a valid session cookie is present the request proceeds normally.
// Otherwise the user is redirected to oauthLoginPath with a "redirect" query
// parameter set to the requested path, so the OAuth callback can send them to
// the right place after login.
func SessionMiddleware(sessions *SessionStore, oauthLoginPath string, logger *zap.SugaredLogger) gin.HandlerFunc {
	return func(c *gin.Context) {
		cookieVal, err := c.Cookie(sessions.CookieName())
		if err == nil && cookieVal != "" {
			if data, ok := sessions.Get(cookieVal); ok {
				claims, err := parseClaimsFromToken(data.AccessToken)
				if err != nil {
					logger.Warnw("failed to parse claims from session token", "error", err)
					// Fall through to redirect below.
				} else {
					setClaims(c, claims, data.AccessToken)
					c.Next()
					return
				}
			}
		}

		redirectTo := oauthLoginPath + "?redirect=" + url.QueryEscape(c.Request.URL.Path)
		c.Redirect(http.StatusFound, redirectTo)
		c.Abort()
	}
}

// setClaims writes the standard UAA context keys used by downstream handlers.
// Access token is stored so that handlers (e.g. /user-api) can forward it.
func setClaims(c *gin.Context, claims *db.UaaClaims, rawToken string) {
	c.Set("user_name", claims.UserName)
	c.Set("scope", claims.Scope)
	c.Set("origin", claims.Origin)
	c.Set("uaa_claim", *claims)
	c.Set("access_token", rawToken)
}

// parseClaimsFromToken decodes the payload of a JWT without verifying its
// signature.  This is intentional: tokens stored in the session were retrieved
// directly from the issuer's token endpoint, so we already trust them.
func parseClaimsFromToken(tokenStr string) (*db.UaaClaims, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode token payload: %w", err)
	}
	var claims db.UaaClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("failed to unmarshal claims: %w", err)
	}
	return &claims, nil
}

// keyFromJKU fetches the JWKS from jku and returns the RSA public key for kid.
func keyFromJKU(jku string, kid string) (*rsa.PublicKey, error) {
	set, err := jwk.Fetch(context.Background(), jku)
	if err != nil {
		return nil, err
	}
	key, ok := set.LookupKeyID(kid)
	if !ok {
		return nil, fmt.Errorf("kid %s not found in JWKS", kid)
	}
	var rsaPubKey rsa.PublicKey
	if err := key.Raw(&rsaPubKey); err != nil {
		return nil, err
	}
	return &rsaPubKey, nil
}
