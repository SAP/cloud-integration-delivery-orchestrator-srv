package main

import (
	"context"
	"crypto/rsa"
	"fmt"
	"strings"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/handler"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/env"
	"mmt-delivery/pkg/tms"
	"mmt-delivery/pkg/xsuaa"
	"mmt-delivery/service"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/jwk"
)

var logger = env.Logger().Desugar()

func main() {
	ctx := context.Background()

	// --- Create long-lived clients ---
	tmsClient, err := tms.NewClient(ctx)
	if err != nil {
		panic("failed to create TMS client: " + err.Error())
	}

	xsuaaClient, err := xsuaa.NewClient(ctx)
	if err != nil {
		panic("failed to create XSUAA client: " + err.Error())
	}

	cpiManager := cpi.NewManager()

	// --- Build service with all injected dependencies ---
	svc := &service.Service{
		DB:     db.Conn(),
		Logger: env.Logger(),
		TMS:    tmsClient,
		CPI: func(ctx context.Context, tenant string) (service.CPIClient, error) {
			return cpiManager.Get(ctx, tenant)
		},
		GetUserEmail: xsuaa.GetUserEmail,
		Notifier:     service.NewDefaultNotifier(),
	}

	// --- Build handler with all injected dependencies ---
	h := handler.NewHandler(
		svc,
		db.Conn(),
		env.Logger(),
		tmsClient,
		cpiManager,
		xsuaaClient,
		env.Destinations(),
	)

	// --- Setup Gin router ---
	router := gin.New()
	router.Use(ginzap.Ginzap(logger, time.RFC3339, true))
	router.Use(ginzap.RecoveryWithZap(logger, true))
	router.Use(AuthMiddleware())

	v1Group := router.Group("/api/v1")
	v2Group := router.Group("/api/v2")

	h.SetupRoutes(v1Group, v2Group, router)

	if err := router.Run(":8080"); err != nil {
		panic(err)
	}
}

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

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		tokenStr := strings.TrimPrefix(auth, "Bearer ")
		token, err := jwt.ParseWithClaims(tokenStr, &db.UaaClaims{}, func(t *jwt.Token) (any, error) {
			jku, _ := t.Header["jku"].(string)
			kid, _ := t.Header["kid"].(string)
			if jku == "" || kid == "" {
				return nil, fmt.Errorf("missing jku or kid in header")
			}
			return keyFromJKU(jku, kid)
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(403, gin.H{"error": "invalid token:" + err.Error()})
			return
		}
		claims := token.Claims.(*db.UaaClaims)

		// Check token expiration
		if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
			c.AbortWithStatusJSON(401, gin.H{"error": "token has expired"})
			return
		}

		c.Set("user_name", claims.UserName)
		c.Set("scope", claims.Scope)
		c.Set("origin", claims.Origin)
		c.Set("uaa_claim", *claims)
		c.Next()
	}
}
