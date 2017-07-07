serverstarter  [![Build Status](https://travis-ci.org/hnakamur/serverstarter.png)](https://travis-ci.org/hnakamur/serverstarter) [![Go Report Card](https://goreportcard.com/badge/github.com/hnakamur/serverstarter)](https://goreportcard.com/report/github.com/hnakamur/serverstarter) [![GoDoc](https://godoc.org/github.com/hnakamur/serverstarter?status.svg)](https://godoc.org/github.com/hnakamur/serverstarter)
=============

serverstarter is a Go package which provides a server starter which can be used to do graceful restart.

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
	srv := &http.Server{}
	go func() { srv.Serve(l) }()

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM)
	for {
		if <-sigC == syscall.SIGTERM {
			srv.Shutdown(context.Background())
			return
		}
	}
}
```

## Credits
Some code of this package is based on [facebookgo/grace: Graceful restart & zero downtime deploy for Go servers.](https://github.com/facebookgo/grace/) Thanks!
