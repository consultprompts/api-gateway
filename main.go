package main

import (
	"log/slog"
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

	rateLimiter := middleware.NewRateLimiter(10, 20)
	router.Use(rateLimiter.Middleware())

	// public routes
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	router.Any("/auth/register", proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/login", proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/refresh", proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/logout", proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/verify-email", proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/verify-email/resend", proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/password/reset-request", proxy.NewReverseProxy(authServiceURL))
	router.Any("/auth/password/reset", proxy.NewReverseProxy(authServiceURL))
	router.GET("/auth/google/login", proxy.NewReverseProxy(authServiceURL))
	router.GET("/auth/google/callback", proxy.NewReverseProxy(authServiceURL))
	router.GET("/.well-known/jwks.json", proxy.NewReverseProxy(authServiceURL))

	// Payment provider webhook — public at the gateway (providers can't send
	// our JWTs); agency-service verifies its shared secret header instead.
	router.POST("/webhooks/payment-success", proxy.NewReverseProxy(agencyServiceURL))

	authorized := router.Group("/")
	authorized.Use(middleware.RequireAuth(jwksClient.PublicKey))
	{
		authorized.GET("/auth/me", proxy.NewReverseProxy(authServiceURL))
		authorized.POST("/auth/roles/assign", proxy.NewReverseProxy(authServiceURL))
		authorized.POST("/auth/roles/remove", proxy.NewReverseProxy(authServiceURL))
		authorized.GET("/auth/users/:id", proxy.NewReverseProxy(authServiceURL))
		authorized.POST("/agency/leads", proxy.NewReverseProxy(agencyServiceURL))
		authorized.PATCH("/agency/leads/:id", proxy.NewReverseProxy(agencyServiceURL))
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
