package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/handler"
	"mmt-delivery/pkg/auth"
	"mmt-delivery/pkg/cas"
	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/env"
	gh "mmt-delivery/pkg/github"
	cpiotel "mmt-delivery/pkg/otel"
	"mmt-delivery/pkg/xsuaa"
	"mmt-delivery/service"
	"mmt-delivery/web"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

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
	xsuaaClient, err := xsuaa.NewClient(ctx)
	if err != nil {
		panic("failed to create XSUAA client: " + err.Error())
	}

	cpiManager := cpi.NewManager(resolver)
	casManager := cas.NewManager(database, resolver)
	tmsSvc := service.NewTmsFactory(database, resolver)

	// --- WebSocket Hub (always active) ---
	hub := service.NewWSHub(env.Logger())

	// --- Build service with all injected dependencies ---
	svc := &service.Service{
		DB:     database,
		Logger: env.Logger(),
		TmsSvc: tmsSvc,
		CAS: func(ctx context.Context, tenantID uint) (service.CasService, error) {
			return casManager.Get(ctx, tenantID)
		},
		CPI: func(ctx context.Context, tenant string) (service.IntegrationService, error) {
			return cpiManager.Get(ctx, tenant)
		},
		GetUserEmail: xsuaa.GetUserEmail,
		Notifier:     service.NewDefaultNotifier(resolver, database),
		Hub:          hub,
		SyncTracker:  service.NewSyncTracker(),
		ProviderDest: resolver,
	}

	// Recover sync goroutines for DRs that were active before restart
	svc.RecoverActiveSyncs()

	// Register SyncTracker goroutine gauge for OTel metrics
	cpiotel.RegisterSyncTrackerGauge(func() int64 {
		return int64(svc.SyncTracker.ActiveCount())
	})

	// --- Build handler with all injected dependencies ---
	gitAppState := gh.NewStateStore(gh.DefaultStateTTL)
	h := handler.NewHandler(svc, database, env.Logger(), cpiManager, xsuaaClient, resolver, hub, gitAppState)

	// --- OAuth2 setup ---
	oauthCfg, err := auth.LoadOAuthConfigFromEnv()
	if err != nil {
		panic("failed to load OAuth config: " + err.Error())
	}

	sessions := auth.NewSessionStore("__cpi_sid", 12*time.Hour)
	oauthHandler := auth.NewOAuthHandler(oauthCfg, sessions, env.Logger())

	// --- Setup Gin router ---
	otelShutdown := cpiotel.Init(env.AppEnv(), "cpi-delivery", env.Logger())
	defer otelShutdown()

	router := gin.New()
	router.Use(otelgin.Middleware("cpi-delivery"))
	router.Use(ginzap.Ginzap(logger, time.RFC3339, true))
	router.Use(ginzap.RecoveryWithZap(logger, true))

	// --- Public routes (no auth) ---
	router.GET("/auth/login", oauthHandler.LoginHandler)
	router.GET("/login/callback", oauthHandler.CallbackHandler)

	// --- Session-required routes ---
	sessionGroup := router.Group("")
	sessionGroup.Use(auth.SessionMiddleware(sessions, "/auth/login", env.Logger()))
	{
		sessionGroup.GET("/user-api/currentUser", handler.HandleCurrentUser(sessions, oauthCfg.UserInfoURL()))
		sessionGroup.GET("/logout", handler.HandleLogout(sessions, oauthCfg.LogoutURL()))
	}

	// --- API routes (session OR Bearer token + scope) ---
	apiGroup := router.Group("")
	apiGroup.Use(auth.Middleware(sessions, env.Logger()))
	{
		v1Group := apiGroup.Group("/api/v1")
		v2Group := apiGroup.Group("/api/v2")
		h.SetupRoutes(v1Group, v2Group, RequireScope)
	}

	// --- Static files / Dev proxy ---
	if viteURL := os.Getenv("VITE_DEV_URL"); viteURL != "" {
		// Development: reverse-proxy to Vite dev server (preserves HMR).
		// Session check ensures unauthenticated users get redirected to XSUAA
		// via top-level navigation (same as production).
		target, err := url.Parse(viteURL)
		if err != nil {
			panic("invalid VITE_DEV_URL: " + err.Error())
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "Vite dev server unreachable: "+err.Error(), 502)
		}
		router.NoRoute(func(c *gin.Context) {
			// Check session before proxying (same as production static serving)
			cookie, err := c.Cookie(sessions.CookieName())
			if err != nil || cookie == "" {
				c.Redirect(302, "/auth/login?redirect="+url.QueryEscape(c.Request.URL.Path))
				return
			}
			if _, ok := sessions.Get(cookie); !ok {
				c.Redirect(302, "/auth/login?redirect="+url.QueryEscape(c.Request.URL.Path))
				return
			}
			proxy.ServeHTTP(c.Writer, c.Request)
		})
		env.Logger().Infof("DEV MODE: proxying static files to Vite at %s", viteURL)
	} else {
		// Production: serve embedded static files
		handler.SetupStaticRoutes(router, web.DistFS, sessions, "/auth/login")
	}

	if err := router.Run(":8080"); err != nil {
		panic(err)
	}
}

// RequireScope checks that the authenticated user has the required scope suffix.
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
