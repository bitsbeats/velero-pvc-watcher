/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	"github.com/bitsbeats/velero-pvc-watcher/internal/controller"
)

// Env holds the environment config
type Env struct {
	MetricsPath       string `default:"/metrics"`
	Port              string `default:"2112"`
	ExcludeAnnotation string `default:"backup.velero.io/backup-volumes-excludes"`
	BackupAnnotation  string `default:"backup.velero.io/backup-volumes"`
}

func main() {
	klog.SetOutput(os.Stdout)
	var env Env
	err := envconfig.Process("", &env)
	if err != nil {
		klog.Fatal(err.Error())
	}

	format := "metricspath: %v\nport: %v\nexcludeannotation: %v\nbackupannotation: %v\n"
	_, err = fmt.Printf(format, env.MetricsPath, env.Port, env.ExcludeAnnotation, env.BackupAnnotation)
	if err != nil {
		klog.Fatal(err.Error())
	}

	config, err := rest.InClusterConfig()
	if err != nil {

		kubeconfig := filepath.Join(homeDir(), ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			klog.Fatal(err.Error())
		}
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatal(err.Error())
	}

	// expose metrics
	http.Handle(env.MetricsPath, promhttp.Handler())
	go func() {
		err := http.ListenAndServe(":"+env.Port, nil)
		if err != nil {
			klog.Fatal(err.Error())
		}
	}()

	//init metrics
	volumeMissing := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "backupmonitor_missing",
			Help: "Kubernetes volume not backed up or excluded for velero restic backups",
		},
		[]string{
			"namespace",
			"owner_kind",
			"owner_name",
		},
	)

	prometheus.MustRegister(volumeMissing)

	// create the pod watcher
	podListWatcher := cache.NewListWatchFromClient(clientset.CoreV1().RESTClient(), "pods", "", fields.Everything())

	// create the workqueue
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	// stored deleted objects
	deletedIndexer := cache.NewIndexer(cache.DeletionHandlingMetaNamespaceKeyFunc, cache.Indexers{})

	// Bind the workqueue to a cache with the help of an informer. This way we make sure that
	// whenever the cache is updated, the pod key is added to the workqueue.
	// Note that when we finally process the item from the workqueue, we might see a newer version
	// of the Pod than the version which was responsible for triggering the update.
	indexer, informer := cache.NewIndexerInformer(podListWatcher, &v1.Pod{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
				_ = deletedIndexer.Delete(obj)
			}
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err == nil {
				queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			// IndexerInformer uses a delta queue, therefore for deletes we have to use this
			// key function.
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				err = deletedIndexer.Add(obj)
				if err != nil {
					klog.Fatal(err.Error())
				}
				queue.Add(key)
			}
		},
	}, cache.Indexers{})

	controller := controller.New(queue, indexer, informer, deletedIndexer, *volumeMissing, env.BackupAnnotation, env.ExcludeAnnotation)

	// Now let's start the controller
	stop := make(chan struct{})
	defer close(stop)
	go controller.Run(1, stop)
	fmt.Println("velero restic backup controller started")

	// Wait forever
	select {}
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
