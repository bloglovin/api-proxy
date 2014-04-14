package main

import (
	"container/list"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// workerSpec is used for unmarshalling the JSON worker specs from etcd.
type workerSpec struct {
	URLString string `json:"url"`
	IsPublic  bool   `json:"public"`
	Auth      string `json:"auth"`
}

// workerInstance is used to keep track of individual workers.
type workerInstance struct {
	Key   string
	URL   *url.URL
	Proxy *httputil.ReverseProxy
}

func newWorkerInstance(key string, jsonBlob string) (*workerInstance, error) {
	var spec *workerSpec
	err := json.Unmarshal([]byte(jsonBlob), &spec)

	if err != nil {
		log.Printf("Failed to unmarshal json for '%v': %v", key, err)
		return nil, err
	}

	url, err := url.Parse(spec.URLString)

	if err != nil {
		log.Printf("Failed to parse url for '%v': %v", key, err)
		return nil, err
	}

	instance := workerInstance{
		Key:   key,
		URL:   url,
		Proxy: httputil.NewSingleHostReverseProxy(url),
	}

	return &instance, nil
}

type workersCommand struct {
	action commandAction
	key    string
	value  *workerInstance
	result chan<- *workerInstance
}

type commandAction int

const (
	remove commandAction = iota
	add
	next
)

type applicationWorkers struct {
	list     *list.List
	current  *list.Element
	commands chan workersCommand
}

func newWorkerList() *applicationWorkers {
	workers := applicationWorkers{
		list:     list.New(),
		commands: make(chan workersCommand),
	}

	go workers.run()
	return &workers
}

func (workers *applicationWorkers) run() {
	for command := range workers.commands {
		switch command.action {
		case add:
			workers.add(command.value)
		case remove:
			workers.remove(command.key)
		case next:
			command.result <- workers.next()
		}
	}
}

func (workers *applicationWorkers) Add(worker *workerInstance) {
	workers.commands <- workersCommand{action: add, value: worker}
}

func (workers *applicationWorkers) add(worker *workerInstance) {
	element := workers.list.PushBack(worker)
	if workers.current == nil {
		workers.current = element
	}
}

func (workers *applicationWorkers) Remove(key string) {
	workers.commands <- workersCommand{action: remove, key: key}
}

func (workers *applicationWorkers) remove(key string) {
	for e := workers.list.Front(); e != nil; e = e.Next() {
		instance := e.Value.(*workerInstance)

		if instance.Key == key {
			if e == workers.current {
				workers.current = e.Next()
			}

			workers.list.Remove(e)

			if workers.current == nil {
				workers.current = workers.list.Front()
			}
		}
	}
}

func (workers *applicationWorkers) Next() *workerInstance {
	reply := make(chan *workerInstance)
	workers.commands <- workersCommand{action: next, result: reply}
	return <-reply
}

func (workers *applicationWorkers) next() *workerInstance {
	if workers.current != nil {
		instance := workers.current.Value.(*workerInstance)
		workers.current = workers.current.Next()
		if workers.current == nil {
			workers.current = workers.list.Front()
		}
		return instance
	}
	return nil
}

func (workers *applicationWorkers) Handle(w http.ResponseWriter, r *http.Request) {
	if workers.current != nil {
		instance := workers.current.Value.(*workerInstance)
		instance.Proxy.ServeHTTP(w, r)
	}

}
