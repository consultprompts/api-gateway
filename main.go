package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"github.com/consultprompts/api-gateway/internal/middleware"
	"github.com/consultprompts/api-gateway/internal/proxy"
	"github.com/consultprompts/api-gateway/pkg/jwks"
	"github.com/consultprompts/api-gateway/pkg/logger"
)

func main() {
	logger.Init()

	if err := godotenv.Load(); err != nil {
		slog.Warn("no .env file found, using existing environment variables")
	}

	authServiceURL := os.Getenv("AUTH_SERVICE_URL")
	agencyServiceURL := os.Getenv("AGENCY_SERVICE_URL")

	jwksClient, err := jwks.NewClient(authServiceURL + "/.well-known/jwks.json")
	if err != nil {
		slog.Error("failed to initialize JWKS client", "error", err)
		os.Exit(1)
	}

	slog.Info("JWKS fetched successfully")

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.SetTrustedProxies(nil)

	// Strip identity headers from every incoming request so they can never be
	// spoofed by a client — RequireAuth re-sets them after JWT validation.
	// Forwarding headers are also stripped: the gateway is the trusted edge,
	// and the reverse proxy re-appends the real client IP to X-Forwarded-For,
	// which downstream services use for per-IP login lockout.
	router.Use(func(c *gin.Context) {
		c.Request.Header.Del("X-User-ID")
		c.Request.Header.Del("X-User-Roles")
		c.Request.Header.Del("X-Forwarded-For")
		c.Request.Header.Del("X-Real-Ip")
		c.Next()
	})

	router.Use(middleware.CORS(os.Getenv("FRONTEND_URL")))
	router.Use(func(c *gin.Context) {
		c.Next()
		slog.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"ip", c.ClientIP(),
		)
	})

	// Cap request bodies well above the largest legitimate payload (5MB logo
	// upload + form fields) so an oversized POST can't exhaust gateway or
	// service memory — without this, nothing stops a multi-GB multipart body
	// from being buffered downstream.
	const maxBodyBytes = 10 << 20
	router.Use(func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		c.Next()
	})

	// Blanket backstop for all traffic.
	rateLimiter := middleware.NewRateLimiter(10, 20)
	router.Use(rateLimiter.Middleware())

	// Tighter per-IP budgets for abuse-prone routes, layered under the
	// blanket limiter. CORS preflights never reach these — the CORS
	// middleware answers OPTIONS itself — so they only count real requests.
	//
	// emailLimiter is shared across every unauthenticated endpoint that
	// triggers an outbound email (registration verification, resend, password
	// reset): one budget covers them combined, so an email bomber can't get
	// 3x the allowance by rotating endpoints. Generous enough for a real
	// signup with a couple of resends; caps a bot at ~125 emails/hour/IP
	// instead of the blanket limiter's ~36,000.
	emailLimiter := middleware.NewRateLimiter(middleware.PerMinute(2), 5)
	// Login gets its own bucket (no email cost, and auth-service already
	// enforces per-IP lockout) so password fumbling can't eat the email budget.
	loginLimiter := middleware.NewRateLimiter(middleware.PerMinute(10), 10)
	// Redeem is an oracle for guessing lead IDs; UUIDs are unguessable in
	// theory, throttled in practice anyway.
	redeemLimiter := middleware.NewRateLimiter(middleware.PerMinute(5), 10)
	// Lead writes carry up to 5MB uploads and fan out notification emails.
	leadWriteLimiter := middleware.NewRateLimiter(middleware.PerMinute(6), 10)

	// public routes
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	router.Any("/auth/register", emailLimiter.Middleware(), proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/login", loginLimiter.Middleware(), proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/refresh", proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/logout", proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/verify-email", proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/verify-email/resend", emailLimiter.Middleware(), proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/password/reset-request", emailLimiter.Middleware(), proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/password/reset", proxy.NewReverseProxy(authServiceURL))
	router.GET("/auth/google/login", proxy.NewReverseProxy(authServiceURL))
	router.GET("/auth/google/callback", proxy.NewReverseProxy(authServiceURL))
	router.GET("/.well-known/jwks.json", proxy.NewReverseProxy(authServiceURL))

	// Payment provider webhook — public at the gateway (providers can't send
	// our JWTs); agency-service verifies its shared secret header instead.
	router.POST("/webhooks/payment-success", proxy.NewReverseProxy(agencyServiceURL))

	// Lead logo — public at the gateway; a plain <img src> can't attach an
	// Authorization header. See agency-service's handler.GetLeadLogo.
	router.GET("/agency/leads/:id/logo", proxy.NewReverseProxy(agencyServiceURL))

	authorized := router.Group("/")
	authorized.Use(middleware.RequireAuth(jwksClient.PublicKey))
	{
		authorized.GET("/auth/me", proxy.NewReverseProxy(authServiceURL))
		authorized.POST("/auth/roles/assign", proxy.NewReverseProxy(authServiceURL))
		authorized.POST("/auth/roles/remove", proxy.NewReverseProxy(authServiceURL))
		authorized.GET("/auth/users/:id", proxy.NewReverseProxy(authServiceURL))
		authorized.POST("/agency/leads", leadWriteLimiter.Middleware(), proxy.NewReverseProxy(agencyServiceURL))
		authorized.POST("/agency/leads/invite", leadWriteLimiter.Middleware(), proxy.NewReverseProxy(agencyServiceURL))
		authorized.POST("/agency/leads/redeem", redeemLimiter.Middleware(), proxy.NewReverseProxy(agencyServiceURL))
		authorized.PATCH("/agency/leads/:id", leadWriteLimiter.Middleware(), proxy.NewReverseProxy(agencyServiceURL))
		authorized.GET("/agency/leads/mine", proxy.NewReverseProxy(agencyServiceURL))
		authorized.GET("/agency/leads", proxy.NewReverseProxy(agencyServiceURL))
		authorized.PATCH("/agency/leads/:id/milestone", proxy.NewReverseProxy(agencyServiceURL))
		authorized.PATCH("/agency/leads/:id/mockup", proxy.NewReverseProxy(agencyServiceURL))
		authorized.PATCH("/agency/leads/:id/complete", proxy.NewReverseProxy(agencyServiceURL))
		authorized.POST("/agency/leads/:id/review", proxy.NewReverseProxy(agencyServiceURL))
		authorized.PATCH("/agency/leads/:id/maintenance", proxy.NewReverseProxy(agencyServiceURL))
		authorized.POST("/agency/leads/:id/pay", proxy.NewReverseProxy(agencyServiceURL))
		authorized.PATCH("/agency/leads/:id/launch", proxy.NewReverseProxy(agencyServiceURL))
		authorized.POST("/agency/leads/:id/request-meeting", proxy.NewReverseProxy(agencyServiceURL))
		authorized.GET("/agency/leads/:id/activity", proxy.NewReverseProxy(agencyServiceURL))
		authorized.PATCH("/agency/leads/:id/suspend", proxy.NewReverseProxy(agencyServiceURL))
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	slog.Info("starting api gateway", "port", port)
	if err := router.Run(":" + port); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
