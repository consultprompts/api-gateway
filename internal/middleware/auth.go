package middleware

import (
	"crypto/rsa"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID string   `json:"sub"`
	Roles  []string `json:"roles"`
	jwt.RegisteredClaims
}

func RequireAuth(getPublicKey func() *rsa.PublicKey) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")

		if !strings.HasPrefix(authHeader, "Bearer ") {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "UNAUTHORIZED",
					"message": "Missing or invalid authorization header",
				},
			})
			c.Abort()
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return getPublicKey(), nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "INVALID_TOKEN",
					"message": "Invalid or expired token",
				},
			})
			c.Abort()
			return
		}

		claims, ok := token.Claims.(*Claims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "INVALID_TOKEN",
					"message": "invalid token claims",
				},
			})
			c.Abort()
			return
		}

		// strip any incoming trusted headers to prevent spoofing
		c.Request.Header.Del("X-User-ID")
		c.Request.Header.Del("X-User-Roles")

		// set trusted headers for downstream services
		c.Request.Header.Set("X-User-ID", claims.UserID)
		c.Request.Header.Set("X-User-Roles", strings.Join(claims.Roles, ","))

		c.Next()
	}
}
