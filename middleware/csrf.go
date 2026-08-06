package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
)

const (
	csrfTokenLength     = 32
	csrfSessionKey      = "csrf_token"
	csrfHeaderName      = "X-CSRF-Token"
	csrfCookieName      = "csrf_token"
)

func GenerateCSRFToken() (string, error) {
	bytes := make([]byte, csrfTokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// CSRFMiddleware validates CSRF token on state-changing requests
// Uses double-submit cookie pattern
func CSRFMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only check on state-changing methods
		if !isStateChangingMethod(c.Request.Method) {
			c.Next()
			return
		}

		// Skip for API key authenticated requests (Bearer token)
		authHeader := c.GetHeader("Authorization")
		authFields := strings.Fields(authHeader)
		if len(authFields) == 2 && strings.EqualFold(authFields[0], "Bearer") {
			c.Next()
			return
		}
		if len(authFields) == 1 && strings.HasPrefix(authFields[0], "sk-") {
			c.Next()
			return
		}

		// Get token from session
		session := sessions.Default(c)
		sessionToken := session.Get(csrfSessionKey)
		if sessionToken == nil {
			// No token in session - generate one for future requests
			token, _ := GenerateCSRFToken()
			session.Set(csrfSessionKey, token)
			_ = session.Save()
			c.Next()
			return
		}

		// Check token from header (preferred) or form
		requestToken := c.GetHeader(csrfHeaderName)
		if requestToken == "" {
			requestToken = c.PostForm("_csrf")
		}
		if requestToken == "" {
			requestToken = c.Query("_csrf")
		}

		if requestToken != sessionToken {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "CSRF token 验证失败，请刷新页面重试",
			})
			return
		}

		c.Next()
	}
}

// CSRFTokenMiddleware injects CSRF token into response for frontend
// Should be applied to routes that render HTML pages or return API responses needing token
func CSRFTokenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		session := sessions.Default(c)
		token := session.Get(csrfSessionKey)
		if token == nil {
			var err error
			token, err = GenerateCSRFToken()
			if err != nil {
				c.Next()
				return
			}
			session.Set(csrfSessionKey, token)
			_ = session.Save()
		}

		// Set in header for AJAX requests
		c.Header(csrfHeaderName, token.(string))

		// Also set as cookie for form submissions (non-HttpOnly for JS access)
		c.SetCookie(csrfCookieName, token.(string), 0, "/", "", false, false)

		c.Next()
	}
}

func isStateChangingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}