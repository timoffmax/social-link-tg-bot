// Command social-link-dashboard serves the owner's statistics dashboard (a
// React page and a JSON API) from the bot's database. It only reads, except
// the Blog tab (blog posts), the Reports tab (queued replies, resolving) and
// the Settings tab (queued change requests the bot applies).
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/timoffmax/social-link-bot/dashboard"
)

// driverName is the database/sql driver registered by lib/pq.
const driverName = "postgres"

// checkTimezoneSQL fails when PostgreSQL does not know the timezone name.
const checkTimezoneSQL = `SELECT now() AT TIME ZONE $1::text`

// Connection pool limits; the dashboard has one viewer.
const (
	maxOpenConns    = 4
	maxIdleConns    = 2
	connMaxIdleTime = 5 * time.Minute
)

// HTTP server timeouts.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 10 * time.Second
	startupPingWait   = 5 * time.Second
)

// allowedHostsLogSeparator joins the allowed hosts in the start-up log line.
const allowedHostsLogSeparator = ", "

func main() {
	err := run()

	if err != nil {
		log.Fatalf("dashboard: %v", err)
	}
}

// run loads the config, opens the database and serves until a signal.
func run() error {
	config, err := dashboard.LoadConfig(os.Getenv)
	var db *sql.DB

	if err == nil {
		db, err = openDatabase(config.DatabaseURL)
	}

	if err == nil {
		defer db.Close()
		err = checkTimezone(db, config.Timezone)
	}

	if err == nil {
		err = serve(config, db)
	}

	return err
}

// openDatabase opens the pool and checks that the database answers.
func openDatabase(databaseURL string) (*sql.DB, error) {
	db, err := sql.Open(driverName, databaseURL)

	if err == nil {
		db.SetMaxOpenConns(maxOpenConns)
		db.SetMaxIdleConns(maxIdleConns)
		db.SetConnMaxIdleTime(connMaxIdleTime)
		err = pingDatabase(db)
	}

	return db, err
}

// pingDatabase waits at most startupPingWait for the database.
func pingDatabase(db *sql.DB) error {
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, startupPingWait)
	defer cancel()
	err := db.PingContext(ctx)

	return err
}

// checkTimezone makes a timezone PostgreSQL rejects fail at start-up
// instead of in every data query.
func checkTimezone(db *sql.DB, timezone string) error {
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, startupPingWait)
	defer cancel()
	_, err := db.ExecContext(ctx, checkTimezoneSQL, timezone)

	if err != nil {
		err = fmt.Errorf("timezone %q is not known to the database: %w", timezone, err)
	}

	return err
}

// serve runs the HTTP server and shuts it down on SIGINT or SIGTERM.
func serve(config dashboard.Config, db *sql.DB) error {
	store := dashboard.NewPGStore(db, config.Timezone)
	assets, err := dashboard.DistFS()

	if err == nil {
		handler := dashboard.NewServer(store, config.Timezone, config.AllowedHosts, assets)
		server := newHTTPServer(config.Addr, handler)
		err = listenUntilSignal(server, config)
	}

	return err
}

// newHTTPServer applies the timeouts.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	result := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	return result
}

// listenUntilSignal opens the listener (TCP or unix socket) and serves until
// it fails or a signal arrives.
func listenUntilSignal(server *http.Server, config dashboard.Config) error {
	listener, err := dashboard.Listen(config)

	if err == nil {
		err = serveUntilSignal(server, listener, config)
	}

	return err
}

// serveUntilSignal serves on listener until it fails or a signal arrives;
// shutting down closes the listener, which removes a unix socket.
func serveUntilSignal(server *http.Server, listener net.Listener, config dashboard.Config) error {
	background := context.Background()
	ctx, stop := signal.NotifyContext(background, os.Interrupt, syscall.SIGTERM)
	defer stop()
	failed := make(chan error, 1)

	go func() {
		failed <- server.Serve(listener)
	}()

	hosts := strings.Join(config.AllowedHosts, allowedHostsLogSeparator)
	address := listenAddress(config)
	log.Printf("dashboard: listening on %s (timezone %s, hosts %s)", address, config.Timezone, hosts)
	var err error

	select {
	case err = <-failed:
	case <-ctx.Done():
		err = shutdown(server)
	}

	if errors.Is(err, http.ErrServerClosed) == true {
		err = nil
	}

	return err
}

// listenAddress names the listener in the start-up log line.
func listenAddress(config dashboard.Config) string {
	result := fmt.Sprintf("http://%s", config.Addr)

	if config.SocketPath != "" {
		result = config.Addr
	}

	return result
}

// shutdown lets running requests finish.
func shutdown(server *http.Server) error {
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, shutdownTimeout)
	defer cancel()
	log.Printf("dashboard: shutting down")
	err := server.Shutdown(ctx)

	return err
}
