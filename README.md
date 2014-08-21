api-proxy
=========

API proxy written in Go

## Build & Run

Install Go `brew install go` / `sudo apt-get install golang`.

Make sure that you have a $GOPATH set up, otherwise, create a folder `mkdir $HOME/go` and add `export GOPATH=$HOME/go` and `export PATH=$GOPATH/bin:$PATH` to your `.profile` or whatever your shell sources to get going.

Then install the etcd depdency: `go get github.com/coreos/go-etcd/etcd`

Then install the api-proxy:

```
mkdir -p "$GOPATH/src/github.com/bloglovin"
git clone git@github.com:bloglovin/api-proxy.git "$GOPATH/src/github.com/bloglovin/api-proxy"
cd "$GOPATH/src/github.com/bloglovin/api-proxy"
go install
```

Run `api-proxy`

PROFIT
