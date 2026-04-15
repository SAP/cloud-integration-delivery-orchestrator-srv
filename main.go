package main

import (
	"context"
	"crypto/rsa"
	"fmt"
	"strings"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/handler"
	"mmt-delivery/pkg/cf"
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

var enableSSE = false

func main() {
	// --- Explicit initialization (no more init() side effects) ---
	if err := env.Init(); err != nil {
		panic("failed to initialize env: " + err.Error())
	}

	logger := env.Logger().Desugar()

	database, err := db.Connect()
	if err != nil {
		panic("failed to connect database: " + err.Error())
	}

	ctx := context.Background()

	// --- Create Destination Service client (provider-side, TTL cache enabled) ---
	resolver, err := cf.NewDestinationServiceClientFromVCAP(env.AppEnv())
	if err != nil {
		panic("failed to create destination service client: " + err.Error())
	}

	// --- Create long-lived clients ---
	tmsClient, err := tms.NewClient(ctx)
	if err != nil {
		panic("failed to create TMS client: " + err.Error())
	}

	xsuaaClient, err := xsuaa.NewClient(ctx)
	if err != nil {
		panic("failed to create XSUAA client: " + err.Error())
	}

	cpiManager := cpi.NewManager(resolver)

	var eventBus *service.EventBus
	if enableSSE {
		eventBus = service.NewEventBus()
	}

	// --- Build service with all injected dependencies ---
	svc := &service.Service{
		DB:     database,
		Logger: env.Logger(),
		TMS:    tmsClient,
		CPI: func(ctx context.Context, tenant string) (service.IntegrationService, error) {
			return cpiManager.Get(ctx, tenant)
		},
		GetUserEmail: xsuaa.GetUserEmail,
		Notifier:     service.NewDefaultNotifier(resolver, database),
		EventBus:     eventBus,
		ProviderDest: resolver,
	}

	// --- Build handler with all injected dependencies ---
	h := handler.NewHandler(
		svc,
		database,
		env.Logger(),
		tmsClient,
		cpiManager,
		xsuaaClient,
		resolver,
		eventBus,
	)

	// Background sync and SSE are coupled: auto-sync only makes sense when
	// real-time push is available; otherwise users trigger sync manually.
	if enableSSE {
		svc.StartBackgroundSync(ctx, 15*time.Second)
	}

	// --- Setup Gin router ---
	router := gin.New()
	router.Use(ginzap.Ginzap(logger, time.RFC3339, true))
	router.Use(ginzap.RecoveryWithZap(logger, true))
	router.Use(AuthMiddleware())

	v1Group := router.Group("/api/v1")
	v2Group := router.Group("/api/v2")

	h.SetupRoutes(v1Group, v2Group, RequireScope)

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
			c.AbortWithStatusJSON(403, gin.H{"message": "invalid token: " + err.Error()})
			return
		}
		claims := token.Claims.(*db.UaaClaims)

		// Check token expiration
		if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
			c.AbortWithStatusJSON(401, gin.H{"message": "token has expired"})
			return
		}

		c.Set("user_name", claims.UserName)
		c.Set("scope", claims.Scope)
		c.Set("origin", claims.Origin)
		c.Set("uaa_claim", *claims)
		c.Next()
	}
}

func RequireScope(requiredScope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, exists := c.Get("uaa_claim")
		if !exists {
			c.AbortWithStatusJSON(403, gin.H{
				"message": "No authentication claims found in request",
			})
			return
		}
		uaaClaims := claims.(db.UaaClaims)
		for _, s := range uaaClaims.Scope {
			if strings.HasSuffix(s, "."+requiredScope) {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(403, gin.H{
			"message": fmt.Sprintf("User '%s' does not have the required scope '%s'. Contact your administrator to assign the appropriate Role Collection.", uaaClaims.UserName, requiredScope),
		})
	}
}
