package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

type HTTPServer struct {
	server      *http.Server
	port        string
	healthCheck *HealthChecker
}

type HealthResponse struct {
	Status    string            `json:"status"`
	Timestamp string            `json:"timestamp"`
	Instance  string            `json:"instance"`
	Version   string            `json:"version"`
	Services  map[string]string `json:"services"`
	Database  string            `json:"database"`
	Error     string            `json:"error,omitempty"`
}

type StatusResponse struct {
	Uptime        string                 `json:"uptime"`
	HealthStatus  string                 `json:"health_status"`
	TelegramBot   string                 `json:"telegram_bot"`
	Database      string                 `json:"database"`
	MediaStats    map[string]interface{} `json:"media_stats,omitempty"`
	PlatformTests map[string]string      `json:"platform_tests,omitempty"`
}

var httpServer *HTTPServer

func InitHTTPServer() error {
	if !config.IsHTTPServerEnabled() {
		log.Println("HTTP server disabled in configuration")
		return nil
	}

	port := config.GetHTTPPort()
	healthCheck := NewHealthChecker()

	httpServer = &HTTPServer{
		port:        port,
		healthCheck: healthCheck,
	}

	mux := http.NewServeMux()

	// Health endpoints
	mux.HandleFunc("/health", httpServer.handleHealth)
	mux.HandleFunc("/status", httpServer.handleStatus)
	mux.HandleFunc("/metrics", httpServer.handleMetrics)

	// Root endpoint
	mux.HandleFunc("/", httpServer.handleRoot)

	httpServer.server = &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("🌐 Starting HTTP server on port %s", port)
		if err := httpServer.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("❌ HTTP server error: %v", err)
		}
	}()

	log.Printf("✅ HTTP server initialized on port %s", port)
	return nil
}

func CloseHTTPServer() error {
	if httpServer != nil && httpServer.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.server.Shutdown(ctx)
	}
	return nil
}

func (h *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	
	response := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Instance:  getInstanceName(),
		Version:   "1.0.0",
		Services:  make(map[string]string),
	}

	// Check database connectivity
	if err := checkDatabaseHealth(); err != nil {
		response.Database = "unhealthy"
		response.Status = "degraded"
		response.Error = fmt.Sprintf("Database error: %v", err)
	} else {
		response.Database = "healthy"
	}

	// Quick platform checks (basic connectivity)
	platformStatus := h.healthCheck.RunQuickCheck()
	if platformStatus {
		response.Services["platforms"] = "healthy"
	} else {
		response.Services["platforms"] = "degraded"
		if response.Status == "ok" {
			response.Status = "degraded"
		}
	}

	// Check Telegram bot token
	if config.GetTgBotToken() == "" {
		response.Services["telegram"] = "misconfigured"
		response.Status = "unhealthy"
		response.Error = "Telegram bot token not configured"
	} else {
		response.Services["telegram"] = "configured"
	}

	// Set appropriate HTTP status code
	statusCode := http.StatusOK
	if response.Status == "degraded" {
		statusCode = http.StatusPartialContent
	} else if response.Status == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding health response: %v", err)
	}

	duration := time.Since(startTime)
	log.Printf("📊 Health check completed in %v - Status: %s", duration, response.Status)
}

func (h *HTTPServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	response := StatusResponse{
		Uptime:       time.Since(time.Now().Add(-24*time.Hour)).String(), // Placeholder
		HealthStatus: "ok",
		TelegramBot:  "running",
		Database:     "connected",
	}

	// Check database and get basic stats
	if err := checkDatabaseHealth(); err != nil {
		response.Database = "disconnected"
		response.HealthStatus = "degraded"
	} else {
		// Get today's media stats as sample
		if stats, err := GetMediaStats(0, "today"); err == nil { // Use 0 for global stats
			response.MediaStats = map[string]interface{}{
				"total_today": stats.Total,
				"photos":      stats.TotalPhotos,
				"videos":      stats.TotalVideos,
			}
		}
	}

	// Run comprehensive platform tests if requested
	if r.URL.Query().Get("detailed") == "true" {
		platformTests := make(map[string]string)
		
		// Run quick tests for each platform
		testResults := h.healthCheck.RunQuickCheck()
		if testResults {
			platformTests["overall"] = "passing"
		} else {
			platformTests["overall"] = "failing"
			response.HealthStatus = "degraded"
		}
		
		response.PlatformTests = platformTests
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding status response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (h *HTTPServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	// Basic Prometheus-style metrics
	w.Header().Set("Content-Type", "text/plain")
	
	fmt.Fprintf(w, "# HELP social_link_bot_health Bot health status (1 = healthy, 0 = unhealthy)\n")
	fmt.Fprintf(w, "# TYPE social_link_bot_health gauge\n")
	
	healthStatus := 1
	if err := checkDatabaseHealth(); err != nil {
		healthStatus = 0
	}
	
	fmt.Fprintf(w, "social_link_bot_health %d\n", healthStatus)
	
	// Database metrics
	fmt.Fprintf(w, "# HELP social_link_bot_database_connected Database connection status\n")
	fmt.Fprintf(w, "# TYPE social_link_bot_database_connected gauge\n")
	fmt.Fprintf(w, "social_link_bot_database_connected %d\n", healthStatus)
	
	// Get media stats for metrics
	if stats, err := GetMediaStats(0, "today"); err == nil {
		fmt.Fprintf(w, "# HELP social_link_bot_media_total_today Total media processed today\n")
		fmt.Fprintf(w, "# TYPE social_link_bot_media_total_today counter\n")
		fmt.Fprintf(w, "social_link_bot_media_total_today %d\n", stats.Total)
		
		fmt.Fprintf(w, "# HELP social_link_bot_photos_today Photos processed today\n")
		fmt.Fprintf(w, "# TYPE social_link_bot_photos_today counter\n")
		fmt.Fprintf(w, "social_link_bot_photos_today %d\n", stats.TotalPhotos)
		
		fmt.Fprintf(w, "# HELP social_link_bot_videos_today Videos processed today\n")
		fmt.Fprintf(w, "# TYPE social_link_bot_videos_today counter\n")
		fmt.Fprintf(w, "social_link_bot_videos_today %d\n", stats.TotalVideos)
	}
}

func (h *HTTPServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	info := map[string]interface{}{
		"service":   "social-link-bot",
		"version":   "1.0.0",
		"status":    "running",
		"endpoints": []string{"/health", "/status", "/metrics"},
		"instance":  getInstanceName(),
	}
	
	json.NewEncoder(w).Encode(info)
}

func checkDatabaseHealth() error {
	db := GetDB()
	if db == nil {
		return fmt.Errorf("database not initialized")
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	return db.PingContext(ctx)
}

func getInstanceName() string {
	if instance := config.GetTgBotToken(); instance != "" {
		// Use last 8 characters of bot token as instance identifier
		if len(instance) > 8 {
			return instance[len(instance)-8:]
		}
		return instance
	}
	return "unknown"
}