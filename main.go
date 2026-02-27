package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"

	"github.com/paragm/topology_discovery_exporter/collector"
	"github.com/paragm/topology_discovery_exporter/db"
	"github.com/paragm/topology_discovery_exporter/discovery"
)

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func main() {
	listenAddr := flag.String("web.listen-address", ":10042", "Address to listen on for web interface and telemetry")
	configFile := flag.String("config.file", "/opt/topology/config.yml", "Path to configuration file")
	logLevel := flag.String("log.level", "info", "Log level: debug, info, warn, error")
	showVersion := flag.Bool("version", false, "Show version and exit")
	discoveryInterval := flag.String("discovery.interval", "15m", "Interval between topology discovery runs")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Print("topology_discovery_exporter"))
		os.Exit(0)
	}

	// Environment variable overrides
	if v := os.Getenv("PORT"); v != "" {
		*listenAddr = ":" + v
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		*logLevel = v
	}
	if v := os.Getenv("CONFIG_FILE"); v != "" {
		*configFile = v
	}
	if v := os.Getenv("DISCOVERY_INTERVAL"); v != "" {
		*discoveryInterval = v
	}

	// Configure structured logging
	var level slog.Level
	switch strings.ToLower(*logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	logger.Info("starting topology discovery exporter", "version", version.Version)

	// Parse discovery interval
	interval, err := time.ParseDuration(*discoveryInterval)
	if err != nil {
		logger.Error("invalid discovery interval", "interval", *discoveryInterval, "error", err)
		os.Exit(1)
	}
	if interval < 30*time.Second {
		logger.Warn("discovery interval too short, clamping to 30s", "requested", interval)
		interval = 30 * time.Second
	}

	// Load configuration
	cfg, err := discovery.LoadConfig(*configFile)
	if err != nil {
		logger.Error("failed to load config", "file", *configFile, "error", err)
		os.Exit(1)
	}
	if err := discovery.ValidateConfig(cfg); err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	logger.Info("loaded configuration", "switches", len(cfg.Switches), "config_file", *configFile)

	// Open database
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		logger.Error("failed to open database", "path", cfg.DBPath, "error", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := database.InitSchema(); err != nil {
		database.Close()
		logger.Error("failed to initialize database schema", "error", err)
		os.Exit(1)
	}

	// Get hostname
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	// Initialize discovery state
	state := &discovery.State{
		SwitchErrors: make(map[string]int64),
	}

	// Create fresh Prometheus registry (no default Go metrics)
	registry := prometheus.NewRegistry()

	// Register MasterCollector
	mc := collector.NewMasterCollector(&collector.Config{
		Logger:         logger,
		Hostname:       hostname,
		DiscoveryState: state,
	})
	registry.MustRegister(mc)

	// Start background discovery ticker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		logger.Info("running initial topology discovery")
		if err := discovery.RunDiscovery(cfg, state, database, logger); err != nil {
			logger.Error("initial discovery failed", "error", err)
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				logger.Info("running scheduled topology discovery")
				if err := discovery.RunDiscovery(cfg, state, database, logger); err != nil {
					logger.Error("scheduled discovery failed", "error", err)
				}
			case <-ctx.Done():
				logger.Info("stopping discovery ticker")
				return
			}
		}
	}()

	// HTTP server
	mux := http.NewServeMux()

	// Metrics endpoint
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}))

	// Health endpoints
	mux.HandleFunc("/-/healthy", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})
	mux.HandleFunc("/-/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})

	// JSON API for current topology
	mux.HandleFunc("/api/v1/topology", func(w http.ResponseWriter, r *http.Request) {
		state.RLock()
		defer state.RUnlock()

		resp := map[string]interface{}{
			"switches":          state.Switches,
			"hosts":             state.Hosts,
			"links":             state.Links,
			"last_run_time":     state.LastRunTime,
			"last_run_duration": state.LastRunDuration.String(),
			"last_run_success":  state.LastRunSuccess,
			"run_count":         state.RunCount,
			"error_count":       state.ErrorCount,
			"data_age_seconds":  time.Since(state.LastRunTime).Seconds(),
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			logger.Error("failed to encode topology response", "error", err)
		}
	})

	// Landing page
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Topology Discovery Exporter</title></head>
<body>
<h1>Topology Discovery Exporter</h1>
<p>Version: %s (branch: %s, revision: %s)</p>
<ul>
  <li><a href="/metrics">Metrics</a></li>
  <li><a href="/api/v1/topology">Topology API</a></li>
  <li><a href="/-/healthy">Health</a></li>
</ul>
</body>
</html>`, version.Version, version.Branch, version.Revision)
	})

	// Wrap mux with security headers middleware
	handler := securityHeadersMiddleware(mux)

	// Graceful shutdown
	server := &http.Server{
		Addr:              *listenAddr,
		Handler:           handler,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("HTTP server shutdown error", "error", err)
		}
	}()

	logger.Info("listening", "address", *listenAddr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("HTTP server error", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped")
}
