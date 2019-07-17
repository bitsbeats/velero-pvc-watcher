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
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/klog"

	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
)

// Env holds the environment config
type Env struct {
	MetricsPath       string `default:"/metrics"`
	Port              string `default:"2112"`
	ExcludeAnnotation string `default:"backup.velero.io/backup-volumes-excludes"`
	BackupAnnotation  string `default:"backup.velero.io/backup-volumes"`
}

// Controller default
type Controller struct {
	indexer           cache.Indexer
	queue             workqueue.RateLimitingInterface
	informer          cache.Controller
	deletedIndexer    cache.Indexer
	volumeMissing     prometheus.GaugeVec
	excludeAnnotation string
	backupAnnotation  string
}

// NewController default
func NewController(queue workqueue.RateLimitingInterface, indexer cache.Indexer, informer cache.Controller, deletedIndexer cache.Indexer, volumeMissing prometheus.GaugeVec, excludeAnnotation string, backupAnnotation string) *Controller {
	return &Controller{
		informer:          informer,
		indexer:           indexer,
		queue:             queue,
		deletedIndexer:    deletedIndexer,
		volumeMissing:     volumeMissing,
		excludeAnnotation: excludeAnnotation,
		backupAnnotation:  backupAnnotation,
	}
}

func (c *Controller) processNextItem() bool {
	// Wait until there is a new item in the working queue
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two pods with the same key are never processed in
	// parallel.
	defer c.queue.Done(key)

	// Invoke the method containing the business logic
	err := c.syncToStdout(key.(string), c.volumeMissing, c.excludeAnnotation, c.backupAnnotation)
	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, key)
	return true
}

// syncToStdout is the business logic of the controller. In this controller it simply prints
// information about the pod to stdout. In case an error happened, it has to simply return the error.
// The retry logic should not be part of the business logic.
func (c *Controller) syncToStdout(key string, volumeMissing prometheus.GaugeVec, excludeAnnotation string, backupAnnotation string) error {

	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		klog.Errorf("Fetching object with key %s from store failed with %v", key, err)
		return err
	}

	if !exists {
		// Below we will warm up our cache with a Pod, so that we will see a delete for one pod
		fmt.Printf("Pod %s does not exist anymore\n", key)

		// Handling cleanup steps
		if obj, exists, err = c.deletedIndexer.GetByKey(key); err == nil && exists {

			fmt.Println("Disabling metric " + obj.(*v1.Pod).Namespace + "/" + obj.(*v1.Pod).OwnerReferences[0].Kind + "/" + obj.(*v1.Pod).OwnerReferences[0].Name)
			disableMetric(volumeMissing, obj.(*v1.Pod).Namespace, obj.(*v1.Pod).OwnerReferences[0].Kind, obj.(*v1.Pod).OwnerReferences[0].Name)
			c.deletedIndexer.Delete(key)
		}

	} else {
		// Note that you also have to check the uid if you have a local controlled resource, which
		// is dependent on the actual instance, to detect that a Pod was recreated with the same name
		fmt.Printf("Controlling backup config for %s\n", obj.(*v1.Pod).GetName())
		if checkBackup(obj.(*v1.Pod), backupAnnotation, excludeAnnotation) {
			fmt.Println("Backup missing enable metric for " + obj.(*v1.Pod).Namespace + "/" + obj.(*v1.Pod).OwnerReferences[0].Kind + "/" + obj.(*v1.Pod).OwnerReferences[0].Name)
			enableMetric(volumeMissing, obj.(*v1.Pod).Namespace, obj.(*v1.Pod).OwnerReferences[0].Kind, obj.(*v1.Pod).OwnerReferences[0].Name)
		}
	}

	return nil
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *Controller) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		c.queue.Forget(key)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.queue.NumRequeues(key) < 5 {
		klog.Infof("Error syncing pod %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.queue.AddRateLimited(key)
		return
	}

	c.queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	runtime.HandleError(err)
	klog.Infof("Dropping pod %q out of the queue: %v", key, err)
}

// Run default
func (c *Controller) Run(threadiness int, stopCh chan struct{}) {
	defer runtime.HandleCrash()

	// Let the workers stop when we are done
	defer c.queue.ShutDown()
	klog.Info("Starting Pod controller")

	go c.informer.Run(stopCh)

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(stopCh, c.informer.HasSynced) {
		runtime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	klog.Info("Stopping Pod controller")
}

func (c *Controller) runWorker() {
	for c.processNextItem() {
	}
}

func main() {

	var env Env
	err := envconfig.Process("", &env)
	if err != nil {
		log.Fatal(err.Error())
	}

	format := "metricspath: %v\nport: %v\nexcludeannotation: %v\nbackupannotation: %v\n"
	_, err = fmt.Printf(format, env.MetricsPath, env.Port, env.ExcludeAnnotation, env.BackupAnnotation)
	if err != nil {
		log.Fatal(err.Error())
	}

	config, err := rest.InClusterConfig()
	if err != nil {

		kubeconfig := filepath.Join(homeDir(), ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			panic(err.Error())
		}
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	// expose metrics
	http.Handle(env.MetricsPath, promhttp.Handler())
	go http.ListenAndServe(":"+env.Port, nil)
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
				deletedIndexer.Delete(obj)
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
				deletedIndexer.Add(obj)
				queue.Add(key)
			}
		},
	}, cache.Indexers{})

	controller := NewController(queue, indexer, informer, deletedIndexer, *volumeMissing, env.BackupAnnotation, env.ExcludeAnnotation)

	// Now let's start the controller
	stop := make(chan struct{})
	defer close(stop)
	go controller.Run(1, stop)
	fmt.Println("Velero restic backup controller started")

	// Wait forever
	select {}
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}

func checkBackup(pod *v1.Pod, excludeAnnotation string, backupAnnotation string) bool {

	// get pod annotations
	annotations := pod.GetAnnotations()

	// range over all volumes
VolumeLoop:
	for _, volume := range pod.Spec.Volumes {

		// check if volume uses persistentVolumeClaim
		if volume.PersistentVolumeClaim != nil {

			fmt.Println("Pod '" + pod.Name + "' uses volume '" + volume.Name + "' from pvc '" + volume.PersistentVolumeClaim.ClaimName + "'")

			// get backup-volumes annotation if present
			if _, ok := annotations[backupAnnotation]; ok {

				// first, clean/remove the comma
				backupString := strings.Replace(annotations[backupAnnotation], ",", " ", -1)

				// convert 'clened' comma separated string to slice
				backupVolumes := strings.Fields(backupString)

				// check if backup is configured
				if contains(backupVolumes, volume.Name) {

					println("backup configured for '" + volume.Name + "'")
					continue VolumeLoop

				}

			}

			// get backup-volumes-excludes annotation if present
			if _, ok := annotations[excludeAnnotation]; ok {

				// first, clean/remove the comma
				backupExcludesString := strings.Replace(annotations[excludeAnnotation], ",", " ", -1)

				// convert 'clened' comma separated string to slice
				backupVolumesExcludes := strings.Fields(backupExcludesString)

				// check if backup is configured
				if contains(backupVolumesExcludes, volume.Name) {

					println("backup exclusion configured for '" + volume.Name + "'")
					continue VolumeLoop

				}

			}

			println(volume.Name + " used but not backed up or excluded")
			return true

		}

	}
	return false
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func enableMetric(volumeMissing prometheus.GaugeVec, namespace string, ownerKind string, ownerName string) {

	volumeMissing.With(prometheus.Labels{"namespace": namespace, "owner_kind": ownerKind, "owner_name": ownerName}).Set(1)

}

func disableMetric(volumeMissing prometheus.GaugeVec, namespace string, ownerKind string, ownerName string) {

	volumeMissing.Delete(prometheus.Labels{"namespace": namespace, "owner_kind": ownerKind, "owner_name": ownerName})

}
