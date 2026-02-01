package sub

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strconv"

	"github.com/alireza0/s-ui/config"
	"github.com/alireza0/s-ui/logger"
	"github.com/alireza0/s-ui/middleware"
	"github.com/alireza0/s-ui/network"
	"github.com/alireza0/s-ui/service"

	"github.com/gin-gonic/gin"
)

type Server struct {
	httpServer *http.Server
	listeners  []net.Listener
	ctx        context.Context
	cancel     context.CancelFunc

	service.SettingService
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

	subPath, err := s.SettingService.GetSubPath()
	if err != nil {
		return nil, err
	}

	subDomain, err := s.SettingService.GetSubDomain()
	if err != nil {
		return nil, err
	}

	if subDomain != "" {
		engine.Use(middleware.DomainValidator(subDomain))
	}

	g := engine.Group(subPath)
	NewSubHandler(g)

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

	certFile, err := s.SettingService.GetSubCertFile()
	if err != nil {
		return err
	}
	keyFile, err := s.SettingService.GetSubKeyFile()
	if err != nil {
		return err
	}
	listen, err := s.SettingService.GetSubListen()
	if err != nil {
		return err
	}
	port, err := s.SettingService.GetSubPort()
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
		logger.Info("Sub server run https on", listener4.Addr())
	} else {
		logger.Info("Sub server run http on", listener4.Addr())
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
			logger.Info("Sub server run https on", listener6.Addr())
		} else {
			logger.Info("Sub server run http on", listener6.Addr())
		}
		s.listeners = append(s.listeners, listener6)
	} else {
		logger.Debug("IPv6 not available for sub server:", err6)
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
