package main

import (
	"net/http"
	"runtime"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

func main() {
	runtime.GOMAXPROCS(1)
	//This section will start the HTTP server and expose
	//any metrics on the /metrics endpoint.
	http.Handle("/metrics", promhttp.Handler())
	log.Info("Opening port", config.Service.ListenPort)
	log.Fatal(http.ListenAndServe(config.Service.ListenPort, nil))
}
