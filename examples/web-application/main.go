package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/kartaladev/doger"
	dogerchi "github.com/kartaladev/doger/http/chi"
)

// Server represents the HTTP server.
type Server struct {
	Config        *Configuration
	DB            *Database
	UserHandler   *UserHandler
	OrderHandler  *OrderHandler
	HealthHandler *HealthHandler
	Logger        *Logger
	injector      doger.Injector
}

func (s *Server) Start() error {
	s.Logger.Log("Starting HTTP server...")

	// Connect to database
	s.Logger.Log("Connecting to database...")
	if err := s.DB.Connect(); err != nil {
		return err
	}

	// Setup router
	router := chi.NewRouter()

	// Middleware
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)

	// Routes
	router.Get("/users/{id}", s.UserHandler.GetUser)
	router.Get("/orders/{id}", s.OrderHandler.GetOrder)
	router.Get("/health", s.HealthHandler.HealthCheck)

	// Debug endpoint for DI container
	dogerchi.Use(router, "/debug/do", s.injector)

	s.Logger.Log(fmt.Sprintf("Server listening on port %d", s.Config.Port))
	s.Logger.Log("Available endpoints:")
	s.Logger.Log("  GET /users/{id} - Get user by ID")
	s.Logger.Log("  GET /orders/{id} - Get order by ID")
	s.Logger.Log("  GET /health - Health check")
	s.Logger.Log("  GET /debug/do - DI container debug UI")

	return http.ListenAndServe(fmt.Sprintf(":%d", s.Config.Port), router) //nolint:gosec
}

func (s *Server) Shutdown() error {
	s.Logger.Log("Shutting down server...")
	return nil
}

func main() {
	// Create injector with options
	injector := doger.NewWithOpts(&doger.InjectorOpts{
		Logf: func(format string, args ...any) {
			fmt.Printf("[DI] "+format+"\n", args...)
		},
		HealthCheckTimeout: 5 * time.Second,
	})

	fmt.Println("=== Web Application Example ===")

	// Register services
	doger.Provide(injector, func(i doger.Injector) (*Configuration, error) {
		return &Configuration{
			AppName: "WebApp",
			Port:    8080,
			Debug:   true,
		}, nil
	})

	doger.Provide(injector, func(i doger.Injector) (*Database, error) {
		config := doger.MustInvoke[*Configuration](i)
		return &Database{
			Config: config,
			URL:    fmt.Sprintf("postgres://localhost:5432/%s", config.AppName),
		}, nil
	})

	doger.Provide(injector, func(i doger.Injector) (*Cache, error) {
		return &Cache{
			Data: make(map[string]any),
		}, nil
	})

	doger.Provide(injector, func(i doger.Injector) (*Logger, error) {
		config := doger.MustInvoke[*Configuration](i)
		level := "INFO"
		if config.Debug {
			level = "DEBUG"
		}
		return &Logger{
			Config: config,
			Level:  level,
		}, nil
	})

	doger.Provide(injector, func(i doger.Injector) (*UserService, error) {
		return &UserService{
			DB:     doger.MustInvoke[*Database](i),
			Cache:  doger.MustInvoke[*Cache](i),
			Logger: doger.MustInvoke[*Logger](i),
		}, nil
	})

	doger.Provide(injector, func(i doger.Injector) (*OrderService, error) {
		return &OrderService{
			DB:     doger.MustInvoke[*Database](i),
			Cache:  doger.MustInvoke[*Cache](i),
			Logger: doger.MustInvoke[*Logger](i),
		}, nil
	})

	doger.Provide(injector, func(i doger.Injector) (*UserHandler, error) {
		return &UserHandler{
			UserService: doger.MustInvoke[*UserService](i),
			Logger:      doger.MustInvoke[*Logger](i),
		}, nil
	})

	doger.Provide(injector, func(i doger.Injector) (*OrderHandler, error) {
		return &OrderHandler{
			OrderService: doger.MustInvoke[*OrderService](i),
			Logger:       doger.MustInvoke[*Logger](i),
		}, nil
	})

	doger.Provide(injector, func(i doger.Injector) (*HealthHandler, error) {
		return &HealthHandler{
			DB:     doger.MustInvoke[*Database](i),
			Cache:  doger.MustInvoke[*Cache](i),
			Logger: doger.MustInvoke[*Logger](i),
		}, nil
	})

	doger.Provide(injector, func(i doger.Injector) (*Server, error) {
		return &Server{
			Config:        doger.MustInvoke[*Configuration](i),
			DB:            doger.MustInvoke[*Database](i),
			UserHandler:   doger.MustInvoke[*UserHandler](i),
			OrderHandler:  doger.MustInvoke[*OrderHandler](i),
			HealthHandler: doger.MustInvoke[*HealthHandler](i),
			Logger:        doger.MustInvoke[*Logger](i),
			injector:      i,
		}, nil
	})

	fmt.Println("=== Service Registration Complete ===")
	fmt.Println("Available services:", injector.ListProvidedServices())

	// Start server
	server := doger.MustInvoke[*Server](injector)

	fmt.Println("\n=== Starting Server ===")
	if err := server.Start(); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}

	fmt.Println("\n=== Shutting Down ===")
	_, err := injector.ShutdownOnSignals()
	if err != nil {
		fmt.Printf("Shutdown error: %v\n", err)
	}
}
