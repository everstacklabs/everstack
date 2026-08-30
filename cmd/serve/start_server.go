package serve

import (
	"context"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/api/http/handlers"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/gorilla/mux"
	"github.com/spf13/viper"
)

func startGatewayServer(ctx context.Context, config *validator.Config, configPath string, embeddedDefaults *EmbeddedDefaults, server chan<- *Server) error {
	router := mux.NewRouter()

	tlsConfig, err := config.Server.TLS.Config()
	if err != nil {
		return err
	}

	// Register pprof handlers BEFORE API initialization if --pprof flag is set
	// This ensures /debug/pprof is registered before /debug (health) claims the prefix
	if viper.GetBool("pprof") {
		logger.Info("Enabling pprof profiling endpoints on /debug/pprof")
		// Register specific routes first (more specific routes should be registered first in gorilla/mux)
		router.HandleFunc("/debug/pprof/", pprof.Index)
		router.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		router.HandleFunc("/debug/pprof/profile", pprof.Profile)
		router.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		router.HandleFunc("/debug/pprof/trace", pprof.Trace)
		router.Handle("/debug/pprof/heap", pprof.Handler("heap"))
		router.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
		router.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
		router.Handle("/debug/pprof/block", pprof.Handler("block"))
		router.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
		router.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
	}

	if viper.GetBool("fastpath") {
		// Register fast-path metrics endpoints (before API initialization to bypass CORS/auth)
		// These endpoints are for monitoring tools (Prometheus) and should not require authentication
		logger.Info("Registering fast-path metrics endpoints (no auth required)")
		router.HandleFunc("/metrics/fastpath", handlers.FastPathMetricsHandler).Methods("GET")
		router.HandleFunc("/debug/fastpath/stats", handlers.FastPathStatsHandler).Methods("GET")
		router.HandleFunc("/debug/fastpath/health", handlers.FastPathHealthHandler).Methods("GET")
	}
	runtime := &GatewayAPIRuntime{}
	_, err = startAPI(ctx, config, configPath, embeddedDefaults, router, runtime)
	if err != nil {
		return err
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	if server != nil {
		server <- &Server{
			Config:    config,
			Router:    router,
			TLSConfig: tlsConfig,
			Shutdown:  shutdown,
		}
		close(server)
	}
	return listenAndServe(ctx, router, uint16(config.Server.Config.Port), uint16(config.Server.Config.ExternalPort), tlsConfig, shutdown, runtime.managedStorageDefaults)
}
