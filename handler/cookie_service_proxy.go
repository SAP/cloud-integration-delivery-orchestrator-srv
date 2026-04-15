package handler

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"mmt-delivery/db"

	"github.com/gin-gonic/gin"
)

// ProxyCookieService proxies requests to the cpi-cookie-service via DestinationResolver.
// The destination name is read from IntegrationConfig("cookie_service").
// Route: /api/v1/cookie-service/*path
func (h *Handler) ProxyCookieService(ctx *gin.Context) {
	config, err := db.GetIntegrationConfig(h.db, "cookie_service")
	if err != nil || !config.Enabled {
		Fail(ctx, 503, "cookie-service integration not configured or disabled")
		return
	}

	dest, err := h.destSvc.GetDestination(ctx, config.DestinationName)
	if err != nil {
		h.logger.Errorf("failed to resolve cookie-service destination '%s': %s", config.DestinationName, err)
		Fail(ctx, 502, fmt.Sprintf("failed to resolve cookie-service destination: %s", err))
		return
	}

	// Build target URL: dest.URL + /api/ + subpath
	subpath := ctx.Param("path")
	targetURL := strings.TrimRight(dest.URL, "/") + "/api/" + strings.TrimLeft(subpath, "/")

	// Create upstream request
	upstreamReq, err := http.NewRequestWithContext(
		ctx.Request.Context(),
		ctx.Request.Method,
		targetURL,
		ctx.Request.Body,
	)
	if err != nil {
		Fail(ctx, 500, fmt.Sprintf("failed to create upstream request: %s", err))
		return
	}

	// Copy query parameters
	upstreamReq.URL.RawQuery = ctx.Request.URL.RawQuery

	// Copy relevant headers
	upstreamReq.Header.Set("Content-Type", ctx.GetHeader("Content-Type"))
	upstreamReq.Header.Set("Accept", "application/json")

	// Forward auth token if present
	if auth := ctx.GetHeader("Authorization"); auth != "" {
		upstreamReq.Header.Set("Authorization", auth)
	}

	// Execute request
	client := &http.Client{Timeout: 90 * 1000_000_000} // 90s timeout (matches frontend max)
	resp, err := client.Do(upstreamReq)
	if err != nil {
		h.logger.Errorf("cookie-service proxy error: %s", err)
		Fail(ctx, 502, fmt.Sprintf("cookie-service request failed: %s", err))
		return
	}
	defer resp.Body.Close()

	// Copy response back to client
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		Fail(ctx, 502, fmt.Sprintf("failed to read cookie-service response: %s", err))
		return
	}

	// Forward response headers
	for k, v := range resp.Header {
		if strings.HasPrefix(strings.ToLower(k), "content-") {
			ctx.Header(k, v[0])
		}
	}

	ctx.Data(resp.StatusCode, resp.Header.Get("Content-Type"), body)
}
