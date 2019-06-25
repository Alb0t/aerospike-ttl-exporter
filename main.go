package main

import (
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"net/http"
)

func main() {
	//This section will start the HTTP server and expose
	//any metrics on the /metrics endpoint.
	http.Handle("/metrics", promhttp.Handler())
	log.Info("Opening port", *listenPort)
	log.Fatal(http.ListenAndServe(*listenPort, nil))
}
