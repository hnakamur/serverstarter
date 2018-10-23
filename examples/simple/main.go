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
	"time"

	"github.com/hnakamur/serverstarter"
)

func main() {
	addr := flag.String("addr", ":8080", "server listen address")
	sleepBeforeServe := flag.Duration("sleep-before-serve", 0, "sleep duration before serve")
	flag.Parse()

	starter := serverstarter.New()
	if starter.IsMaster() {
		l, err := net.Listen("tcp", *addr)
		if err != nil {
			log.Fatalf("failed to listen %s; %v", *addr, err)
		}
		log.Printf("master pid=%d start RunMaster", os.Getpid())
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
		// We received an interrupt signal, shut down.
		if err := srv.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			log.Printf("http(s) server Shutdown: %v", err)
		}
		close(idleConnsClosed)
		log.Printf("closed idleConnsClosed")
	}()

	if *sleepBeforeServe > 0 {
		time.Sleep(*sleepBeforeServe)
	}

	if err := starter.SendReady(); err != nil {
		log.Printf("failed to send ready: %v", err)
	}
	log.Printf("worker pid=%d http server start Serve", os.Getpid())
	if err := srv.Serve(l); err != http.ErrServerClosed {
		// Error starting or closing listener:
		log.Printf("http server Serve: %v", err)
	}
	<-idleConnsClosed
	log.Printf("exiting pid=%d", os.Getpid())
}
