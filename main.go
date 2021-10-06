package main

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"bitsbeats/velero-pvc-watcher/watcher"
)

const (
	ListenAddr = ":2121"
)

var ()

func main() {
	clientset, err := loadClientset()
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

// load matching clientset
func loadClientset() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err == rest.ErrNotInCluster {
		log.Printf("using out of cluster config...")
		home := homedir.HomeDir()
		kubeconfig := filepath.Join(home, ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return nil, fmt.Errorf("unable to load config: %w", err)
	}

	// load k8s config
	log.Printf("loading k8s config")
	return kubernetes.NewForConfig(config)

}
