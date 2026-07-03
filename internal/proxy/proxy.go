package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gin-gonic/gin"
)

func NewReverseProxy(target string) gin.HandlerFunc {
	targetURL, err := url.Parse(target)
	if err != nil {
		panic("invalid proxy target URL: " + target)
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"success":false,"error":{"code":"BAD_GATEWAY","message":"service unavailable"}}`))
	}

	return func(c *gin.Context) {
		proxy.ServeHTTP(c.Writer, c.Request)
	}
}
