package main

import (
	"flag"
	"fmt"
	"github.com/coreos/go-etcd/etcd"
	"log"
	"net/http"
	"strings"
)

type applicationMap map[string]*applicationWorkers

func keyToDomain(key string, baseHost string) string {
	segments := strings.Split(key, "/")
	return fmt.Sprintf("%v.%v.%v", segments[3], segments[2], baseHost)
}

func main() {
	var port int
	var etcdHost string
	var host string

	flag.IntVar(&port, "port", 1080, "Port to run the proxy on")
	flag.StringVar(&etcdHost, "etcd", "http://127.0.0.1:4001", "Url to the etcd API")
	flag.StringVar(&host, "baseHost", fmt.Sprintf("api.dev:%v", port), "Base host for API calls")

	flag.Parse()

	etcdClient := etcd.NewClient([]string{etcdHost})
	apiInfo, err := etcdClient.Get("api", false, true)

	// Start a watch for changes
	changes := make(chan *etcd.Response)
	endWatch := make(chan bool)
	go func() {
		etcdClient.Watch("api", 0, true, changes, endWatch)
	}()

	applications := applicationMap{}

	for _, appDir := range apiInfo.Node.Nodes {
		for _, appVersion := range appDir.Nodes {
			instances := newWorkerList()
			domain := keyToDomain(appVersion.Key, host)
			applications[domain] = instances

			for _, appInstance := range appVersion.Nodes {
				instance, err := newWorkerInstance(appInstance.Key, appInstance.Value)

				if err != nil {
					log.Printf("Failed to register application %v", appInstance.Key)
					continue
				}

				log.Printf("Now handling domain %v with %#v", domain, appInstance.Key)
				instances.Add(instance)
			}
		}
	}

	// React to changes
	go func() {
		for {
			change := <-changes
			domain := keyToDomain(change.Node.Key, host)
			instances := applications[domain]

			if instances == nil {
				instances = newWorkerList()
				applications[domain] = instances
			}

			if change.Action == "delete" || change.Action == "expire" {
				instances.Remove(change.Node.Key)
			} else if change.Action == "create" {
				instance, err := newWorkerInstance(change.Node.Key, change.Node.Value)

				if err != nil {
					log.Printf("Failed to register application %v", change.Node.Key)
				} else {
					instances.Add(instance)
				}
			}
		}
	}()

	if err != nil {
		log.Fatalf("Failed to get worker listing from etcd: %v", err)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {

		workers := applications[r.Host]
		if workers == nil {
			http.Error(w, "The API is unavailable", http.StatusBadGateway)
			return
		}

		instance := workers.Next()
		if instance == nil {
			http.Error(w, "The API is unavailable", http.StatusBadGateway)
			return
		}

		instance.Proxy.ServeHTTP(w, r)
	})
	serverErr := http.ListenAndServe(fmt.Sprintf(":%v", port), nil)
	if serverErr != nil {
		log.Fatalf("Failed start server: %v", serverErr)
	}
}
