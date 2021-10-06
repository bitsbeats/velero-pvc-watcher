package main

import (
	"log"
	"net/http"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"bitsbeats/velero-pvc-watcher/watcher"
)

const (
	ListenAddr = ":2121"
)

var ()

func main() {
	config, err := clientcmd.BuildConfigFromFlags("", "/home/stefan/.kube/config")
	if err != nil {
		log.Fatalf("unable to load local config: %s", err)
	}

	// load k8s config
	log.Printf("loading k8s config")
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("unable to connect to kubernetes: %s", err)
	}

	// start informer factory
	stopper := make(chan struct{}, 1)
	factory := informers.NewSharedInformerFactory(clientset, 1*time.Hour)
	factory.Start(stopper)

	log.Printf("connecting to k8s and warm-up caches")
	w := watcher.NewWatcher(factory, stopper)
	w.Run(stopper)

	err = prometheus.Register(w)
	if err != nil {
		log.Fatalf("unable to register prometheus metrics: %s", err)
	}
	http.Handle("/metrics", promhttp.Handler())
	log.Printf("listening on %s", ListenAddr)
	http.ListenAndServe(ListenAddr, nil)
}
