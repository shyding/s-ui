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
	listeners      []net.Listener
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

	s.httpServer = &http.Server{
		Handler: engine,
	}

	// Create listeners for both IPv4 and IPv6
	portStr := strconv.Itoa(port)
	
	// IPv4 listener
	listenAddr4 := net.JoinHostPort(listen, portStr)
	listener4, err := net.Listen("tcp4", listenAddr4)
	if err != nil {
		return err
	}
	
	// Apply TLS if configured
	if certFile != "" || keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			listener4.Close()
			return err
		}
		c := &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
		listener4 = network.NewAutoHttpsListener(listener4)
		listener4 = tls.NewListener(listener4, c)
		logger.Info("web server run https on", listener4.Addr())
	} else {
		logger.Info("web server run http on", listener4.Addr())
	}
	s.listeners = append(s.listeners, listener4)

	// IPv6 listener (optional, don't fail if IPv6 is not available)
	listen6 := "::"
	if listen != "" && listen != "0.0.0.0" {
		listen6 = listen // Use configured address if it's not the default
	}
	listenAddr6 := net.JoinHostPort(listen6, portStr)
	listener6, err6 := net.Listen("tcp6", listenAddr6)
	if err6 == nil {
		if certFile != "" || keyFile != "" {
			cert, _ := tls.LoadX509KeyPair(certFile, keyFile)
			c := &tls.Config{
				Certificates: []tls.Certificate{cert},
			}
			listener6 = network.NewAutoHttpsListener(listener6)
			listener6 = tls.NewListener(listener6, c)
			logger.Info("web server run https on", listener6.Addr())
		} else {
			logger.Info("web server run http on", listener6.Addr())
		}
		s.listeners = append(s.listeners, listener6)
	} else {
		logger.Debug("IPv6 not available:", err6)
	}

	// Serve on all listeners
	for _, listener := range s.listeners {
		go func(l net.Listener) {
			s.httpServer.Serve(l)
		}(listener)
	}

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
	for _, listener := range s.listeners {
		if listener != nil {
			if closeErr := listener.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}
	}
	return err
}

func (s *Server) GetCtx() context.Context {
	return s.ctx
}
