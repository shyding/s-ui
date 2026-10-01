package web

import (
	"context"
	"crypto/tls"
	"embed"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/alireza0/s-ui/api"
	"github.com/alireza0/s-ui/config"
	"github.com/alireza0/s-ui/logger"
	"github.com/alireza0/s-ui/middleware"
	"github.com/alireza0/s-ui/network"
	"github.com/alireza0/s-ui/service"

	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

//go:embed *
var content embed.FS

type Server struct {
	httpServer     *http.Server
	listener       net.Listener
	ctx            context.Context
	cancel         context.CancelFunc
	settingService service.SettingService
}

func NewServer() *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *Server) initRouter() (*gin.Engine, error) {
	if config.IsDebug() {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.DefaultWriter = io.Discard
		gin.DefaultErrorWriter = io.Discard
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.Default()

	// Load the HTML template
	t := template.New("").Funcs(engine.FuncMap)
	template, err := t.ParseFS(content, "html/index.html")
	if err != nil {
		return nil, err
	}
	engine.SetHTMLTemplate(template)

	base_url, err := s.settingService.GetWebPath()
	if err != nil {
		return nil, err
	}

	webDomain, err := s.settingService.GetWebDomain()
	if err != nil {
		return nil, err
	}

	if webDomain != "" {
		engine.Use(middleware.DomainValidator(webDomain))
	}

	secret, err := s.settingService.GetSecret()
	if err != nil {
		return nil, err
	}

	engine.Use(gzip.Gzip(gzip.DefaultCompression))
	assetsBasePath := base_url + "assets/"

	store := cookie.NewStore(secret)
	engine.Use(sessions.Sessions("s-ui", store))

	engine.Use(func(c *gin.Context) {
		uri := c.Request.RequestURI
		if strings.HasPrefix(uri, assetsBasePath) {
			c.Header("Cache-Control", "max-age=31536000")
		}
	})

	// Serve the assets folder
	assetsFS, err := fs.Sub(content, "html/assets")
	if err != nil {
		panic(err)
	}

	engine.StaticFS(assetsBasePath, http.FS(assetsFS))

	group_apiv2 := engine.Group(base_url + "apiv2")
	apiv2 := api.NewAPIv2Handler(group_apiv2)

	group_api := engine.Group(base_url + "api")
	api.NewAPIHandler(group_api, apiv2)

	// Localhost-only health check trigger (no auth, for automation).
	// Only accepts connections from 127.0.0.1/::1. Used by ops scripts
	// to trigger health checks without panel login.
	engine.POST(base_url+"local/triggerHealthCheck", func(c *gin.Context) {
		// Security: check the actual TCP connection source (RemoteAddr), not
		// headers (ClientIP trusts X-Forwarded-For which can be spoofed).
		// Only allow true loopback connections.
		remoteAddr := c.Request.RemoteAddr
		host, _, err := net.SplitHostPort(remoteAddr)
		if err != nil {
			host = remoteAddr
		}
		if host != "127.0.0.1" && host != "::1" {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "msg": "forbidden: localhost only"})
			return
		}
		if service.IsEgressCheckRunning() || service.IsNodeCheckRunning() {
			c.JSON(http.StatusConflict, gin.H{"success": false, "msg": "健康检查正在运行中", "running": true})
			return
		}
		egressOk := service.TriggerEgressHealthCheck()
		nodeOk := service.TriggerNodeHealthCheck()
		if !egressOk && !nodeOk {
			c.JSON(http.StatusConflict, gin.H{"success": false, "msg": "健康检查正在运行中", "running": true})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "msg": "健康检查已在后台启动", "running": true})
	})

	// Serve index.html as the entry point
	// Handle all other routes by serving index.html
	engine.NoRoute(func(c *gin.Context) {
		if c.Request.URL.Path == strings.TrimSuffix(base_url, "/") {
			c.Redirect(http.StatusTemporaryRedirect, base_url)
			return
		}
		if !strings.HasPrefix(c.Request.URL.Path, base_url) {
			c.String(404, "")
			return
		}
		if c.Request.URL.Path != base_url+"login" && !api.IsLogin(c) {
			c.Redirect(http.StatusTemporaryRedirect, base_url+"login")
			return
		}
		if c.Request.URL.Path == base_url+"login" && api.IsLogin(c) {
			c.Redirect(http.StatusTemporaryRedirect, base_url)
			return
		}
		c.HTML(http.StatusOK, "index.html", gin.H{"BASE_URL": base_url})
	})

	return engine, nil
}

func (s *Server) Start() (err error) {
	//This is an anonymous function, no function name
	defer func() {
		if err != nil {
			s.Stop()
		}
	}()

	engine, err := s.initRouter()
	if err != nil {
		return err
	}

	certFile, err := s.settingService.GetCertFile()
	if err != nil {
		return err
	}
	keyFile, err := s.settingService.GetKeyFile()
	if err != nil {
		return err
	}
	listen, err := s.settingService.GetListen()
	if err != nil {
		return err
	}
	port, err := s.settingService.GetPort()
	if err != nil {
		return err
	}
	listenAddr := net.JoinHostPort(listen, strconv.Itoa(port))
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	if certFile == "" || keyFile == "" {
		if _, err1 := os.Stat("/usr/local/s-ui/certs/fullchain.pem"); err1 == nil {
			if _, err2 := os.Stat("/usr/local/s-ui/certs/privkey.pem"); err2 == nil {
				certFile = "/usr/local/s-ui/certs/fullchain.pem"
				keyFile = "/usr/local/s-ui/certs/privkey.pem"
			}
		}
	}

	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err == nil {
			c := &tls.Config{
				Certificates: []tls.Certificate{cert},
			}
			listener = network.NewDualHttpHttpsListener(listener, c)
			logger.Info("Web server run http/https dual mode on", listener.Addr())
		} else {
			logger.Warningf("Failed to load TLS cert for web server (%v), falling back to plain HTTP", err)
			logger.Info("Web server run http on", listener.Addr())
		}
	} else {
		logger.Info("Web server run http on", listener.Addr())
	}
	s.listener = listener

	s.httpServer = &http.Server{
		Handler: engine,
	}

	go func() {
		s.httpServer.Serve(listener)
	}()

	return nil
}

func (s *Server) Stop() error {
	s.cancel()
	var err error
	if s.httpServer != nil {
		err = s.httpServer.Shutdown(s.ctx)
		if err != nil {
			return err
		}
	}
	if s.listener != nil {
		err = s.listener.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) GetCtx() context.Context {
	return s.ctx
}
