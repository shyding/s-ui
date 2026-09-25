package sub

import (
	"strings"

	"github.com/alireza0/s-ui/logger"
	"github.com/alireza0/s-ui/service"

	"github.com/gin-gonic/gin"
)

type SubHandler struct {
	service.SettingService
	SubService
	JsonService
	ClashService
}

func NewSubHandler(g *gin.RouterGroup) {
	a := &SubHandler{}
	a.initRouter(g)
}

func (s *SubHandler) initRouter(g *gin.RouterGroup) {
	g.GET("/:subid", s.subs)
	g.HEAD("/:subid", s.subHeaders)
}

func (s *SubHandler) subs(c *gin.Context) {
	var headers []string
	var result *string
	var err error
	subId := c.Param("subid")
	format, isFormat := c.GetQuery("format")

	if !isFormat {
		ua := strings.ToLower(c.GetHeader("User-Agent"))
		if strings.Contains(ua, "clash") || strings.Contains(ua, "mihomo") || strings.Contains(ua, "stash") || strings.Contains(ua, "meta") {
			format = "clash"
			isFormat = true
		} else if strings.Contains(ua, "sing-box") || strings.Contains(ua, "sfi") || strings.Contains(ua, "sfa") || strings.Contains(ua, "sfm") {
			format = "json"
			isFormat = true
		}
	}

	if isFormat {
		switch format {
		case "json":
			result, headers, err = s.JsonService.GetJson(subId, format)
		case "clash":
			result, headers, err = s.ClashService.GetClash(subId)
		}
		if err != nil || result == nil {
			logger.Error(err)
			c.String(400, "Error!")
			return
		}
	} else {
		result, headers, err = s.SubService.GetSubs(subId)
		if err != nil || result == nil {
			logger.Error(err)
			c.String(400, "Error!")
			return
		}
	}

	s.addHeaders(c, headers)

	if format == "clash" {
		c.Header("Content-Type", "text/yaml; charset=utf-8")
	} else if format == "json" {
		c.Header("Content-Type", "application/json; charset=utf-8")
	} else {
		c.Header("Content-Type", "text/plain; charset=utf-8")
	}

	c.String(200, *result)
}

func (s *SubHandler) subHeaders(c *gin.Context) {
	subId := c.Param("subid")
	client, err := s.SubService.getClientBySubId(subId)
	if err != nil {
		logger.Error(err)
		c.String(400, "Error!")
		return
	}

	headers := s.SubService.getClientHeaders(client)
	s.addHeaders(c, headers)

	c.Status(200)
}

func (s *SubHandler) addHeaders(c *gin.Context, headers []string) {
	c.Writer.Header().Set("Subscription-Userinfo", headers[0])
	c.Writer.Header().Set("Profile-Update-Interval", headers[1])
	c.Writer.Header().Set("Profile-Title", headers[2])
}
