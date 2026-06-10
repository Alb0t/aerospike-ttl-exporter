package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/carlescere/scheduler"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

func main() {
	// Initialize config, metrics, Aerospike connection, and scheduler.
	jobs := setup()
	//This section will start the HTTP server and expose
	//any metrics on the /metrics endpoint.
	http.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{Addr: config.Service.ListenPort}
	go func() {
		log.Info("Opening port", config.Service.ListenPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	waitForShutdown(srv, jobs)
}

// waitForShutdown blocks until SIGINT/SIGTERM, then stops the scheduler jobs and
// shuts the HTTP server down with a short timeout. It is read-only: it only halts
// timers and the listener, never touching Aerospike. Exits the process cleanly.
func waitForShutdown(srv *http.Server, jobs []*scheduler.Job) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan
	log.Info("Received signal, shutting down: ", sig)

	for _, job := range jobs {
		if job != nil && job.Quit != nil {
			close(job.Quit)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("HTTP server shutdown error: ", err)
	}
	log.Info("Shutdown complete")
	os.Exit(0)
}
