package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/coreos/go-etcd/etcd"
)

type applicationMap map[string]*applicationWorkers

func keyToDomain(key string, separator string, baseHost string) string {
	segments := strings.Split(key, "/")
	return strings.Join([]string{segments[3], segments[2], baseHost}, separator)
}

func main() {
	var port int
	var etcdHost string
	var host string
	var apiPath string
	var separator string

	flag.IntVar(&port, "port", 1080, "Port to run the proxy on")
	flag.StringVar(&etcdHost, "etcd", "http://127.0.0.1:4001", "Url to the etcd API")
	flag.StringVar(&apiPath, "etcdPath", "api", "The path to the node containing the api entries")
	flag.StringVar(&host, "baseHost", fmt.Sprintf("api.dev:%v", port), "Base host for API calls")
	flag.StringVar(&separator, "hostSeparator", "-", "Separator to use when constructing host names")

	flag.Parse()

	etcdClient := etcd.NewClient([]string{etcdHost})
	apiInfo, err := etcdClient.Get(apiPath, false, true)

	if err != nil {
		log.Fatalf("Failed to fetch api info from '%v': %v", apiPath, err)
	}

	// Start a watch for changes
	changes := make(chan *etcd.Response)
	endWatch := make(chan bool)
	go func() {
		etcdClient.Watch(apiPath, 0, true, changes, endWatch)
	}()

	applications := applicationMap{}

	if !apiInfo.Node.Dir {
		log.Fatalf("The api node '%v' in etcd is not a directory", apiPath)
	}

	for _, appDir := range apiInfo.Node.Nodes {
		if !appDir.Dir {
			log.Fatalf("The application node '%v' in etcd is not a directory", appDir.Key)
		}

		for _, appVersion := range appDir.Nodes {
			if !appDir.Dir {
				log.Fatalf("The application version node '%v' in etcd is not a directory", appDir.Key)
			}

			instances := newWorkerList()
			domain := keyToDomain(appVersion.Key, separator, host)
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
			if change.Node == nil || change.Node.Dir {
				continue
			}

			domain := keyToDomain(change.Node.Key, separator, host)
			instances := applications[domain]

			if instances == nil {
				instances = newWorkerList()
				applications[domain] = instances
			}

			if change.Action == "delete" || change.Action == "expire" {
				instances.Remove(change.Node.Key)
				log.Printf("Removed application %v", change.Node.Key)
			} else if change.Action == "create" {
				instance, err := newWorkerInstance(change.Node.Key, change.Node.Value)

				if err != nil {
					log.Printf("Failed to register application %v", change.Node.Key)
				} else {
					instances.Add(instance)
					log.Printf("Added application %v", change.Node.Key)
				}
			}
		}
	}()

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
