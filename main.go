package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-etcd/etcd"
)

type applicationMap map[string]*applicationWorkers

type appConfig struct {
	Port      int
	EtcdHost  string
	Host      string
	APIPath   string
	Separator string
}

func keyToDomain(key string, separator string, baseHost string) string {
	segments := strings.Split(key, "/")
	return strings.Join([]string{segments[3], segments[2], baseHost}, separator)
}

func main() {
	var config appConfig

	flag.IntVar(&config.Port, "port", 1080, "Port to run the proxy on")
	flag.StringVar(&config.EtcdHost, "etcd", "http://127.0.0.1:4001", "Url to the etcd API")
	flag.StringVar(&config.APIPath, "etcdPath", "api", "The path to the node containing the api entries")
	flag.StringVar(&config.Host, "baseHost", fmt.Sprintf("api.dev:%v", config.Port), "Base host for API calls")
	flag.StringVar(&config.Separator, "hostSeparator", "-", "Separator to use when constructing host names")

	flag.Parse()

	etcdClient := etcd.NewClient([]string{config.EtcdHost})
	apiInfo, err := etcdClient.Get(config.APIPath, false, true)

	if err != nil {
		log.Fatalf("Failed to fetch api info from '%v': %v", config.APIPath, err)
	}
	applications := applicationMap{}

	go func() {
		for {
			err := watch(etcdClient, &applications, config)
			if err != nil {
				log.Printf("Connection error: %v", err)
				log.Printf("Attempting recovery in %d seconds", 5)
				time.Sleep(time.Second * 5)
				log.Print("Connecting to etcd")
			}
		}
	}()

	if !apiInfo.Node.Dir {
		log.Fatalf("The api node '%v' in etcd is not a directory", config.APIPath)
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
			domain := keyToDomain(appVersion.Key, config.Separator, config.Host)
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

	serverErr := http.ListenAndServe(fmt.Sprintf(":%v", config.Port), nil)
	if serverErr != nil {
		log.Fatalf("Failed start server: %v", serverErr)
	}
}

func watch(etcdClient *etcd.Client, applications *applicationMap, config appConfig) error {
	// Start a watch for changes
	changes := make(chan *etcd.Response)
	endWatch := make(chan bool)

	// React to changes
	go func() {
		for {
			change := <-changes

			if change == nil {
				return
			}

			if change.Node.Dir {
				continue
			}

			domain := keyToDomain(change.Node.Key, config.Separator, config.Host)
			instances := (*applications)[domain]

			if instances == nil {
				instances = newWorkerList()
				(*applications)[domain] = instances
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

	_, err := etcdClient.Watch(config.APIPath, 0, true, changes, endWatch)
	return err
}
