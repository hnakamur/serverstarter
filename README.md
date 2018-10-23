serverstarter  [![Build Status](https://travis-ci.org/hnakamur/serverstarter.png)](https://travis-ci.org/hnakamur/serverstarter) [![Go Report Card](https://goreportcard.com/badge/github.com/hnakamur/serverstarter)](https://goreportcard.com/report/github.com/hnakamur/serverstarter) [![GoDoc](https://godoc.org/github.com/hnakamur/serverstarter?status.svg)](https://godoc.org/github.com/hnakamur/serverstarter)
=============

serverstarter is a Go package which provides a server starter which can be used to do graceful restart.

## A basic example

An example HTTP server which supports graceful restart.

```
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/hnakamur/serverstarter"
)

func main() {
	addr := flag.String("addr", ":8080", "server listen address")
	flag.Parse()

	starter := serverstarter.New()
	if starter.IsMaster() {
		l, err := net.Listen("tcp", *addr)
		if err != nil {
			log.Fatalf("failed to listen %s; %v", *addr, err)
		}
		if err = starter.RunMaster(l); err != nil {
			log.Fatalf("failed to run master; %v", err)
		}
		return
	}

	listeners, err := starter.Listeners()
	if err != nil {
		log.Fatalf("failed to get listeners; %v", err)
	}
	l := listeners[0]

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "from pid %d.\n", os.Getpid())
	})

	srv := http.Server{}

	idleConnsClosed := make(chan struct{})
	go func() {
		sigterm := make(chan os.Signal, 1)
		signal.Notify(sigterm, syscall.SIGTERM)
		<-sigterm
		log.Printf("received sigterm")

		ctx, cancel := context.WithTimeout(context.Background(),
			*shutdownTimeout)
		defer cancel()
		srv.SetKeepAlivesEnabled(false)
		if err := srv.Shutdown(ctx); err != nil {
			// Error from closing listeners, or context timeout:
			log.Printf("cannot gracefully shut down the server: %v", err)
		}
		close(idleConnsClosed)
		log.Printf("closed idleConnsClosed")
	}()

	if err := starter.SendReady(); err != nil {
		log.Printf("failed to send ready: %v", err)
	}
	if err := srv.Serve(l); err != http.ErrServerClosed {
		// Error starting or closing listener:
		log.Printf("http server Serve: %v", err)
	}
	<-idleConnsClosed
}
```

## A more advanced example

An example server which listens HTTP/1.1 and HTTP/2.0 ports simultaneously and
supports graceful restart.

### Build and run server

```
cd examples/graceserver
go build -race
./graceserver -http=:8080 -https=:8443 -pidfile=graceserver.pid
```

### Keep repeating graceful restarts

In another terminal, run the following command.

```
cd examples/graceserver
watch -n 2 "kill -HUP $(cat graceserver.pid)"
```

### Load test using github.com/tsenart/vegeta

In another terminal, run the following command.

```
$ go get -u github.com/tsenart/vegeta
$ printf "GET http://127.0.0.1:9090/\nGET https://127.0.0.1:9443/\n" | vegeta attack -duration=10s -rate=100 -insecure | vegeta report
```

## Credits

* Some code of this package is based on [facebookgo/grace: Graceful restart & zero downtime deploy for Go servers.](https://github.com/facebookgo/grace/) and [cloudflare/tableflip: Graceful process restarts in Go](https://github.com/cloudflare/tableflip)
* `examles/graceserver/main.go` and is based on [Go1.8のGraceful Shutdownとgo-gracedownの対応 - Shogo's Blog](https://shogo82148.github.io/blog/2017/01/21/golang-1-dot-8-graceful-shutdown/).

Thanks!
