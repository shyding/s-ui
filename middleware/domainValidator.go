package middleware

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func DomainValidator(domain string) gin.HandlerFunc {
	return func(c *gin.Context) {
		host := c.Request.Host
		if colonIndex := strings.LastIndex(host, ":"); colonIndex != -1 {
			host, _, _ = net.SplitHostPort(c.Request.Host)
		}

		if domain == "" {
			c.Next()
			return
		}

		allowed := false
		domains := strings.FieldsFunc(domain, func(r rune) bool {
			return r == ',' || r == ';' || r == ' '
		})
		for _, d := range domains {
			if strings.TrimSpace(d) == host {
				allowed = true
				break
			}
		}

		if host == "dash.icta.top" || host == "sub.icta.top" {
			allowed = true
		}

		if !allowed {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}

		c.Next()
	}
}
