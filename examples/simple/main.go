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
	startDelay := flag.Duration("start-delay", 0, "delay duration before start accepting requests")
	handleDelay := flag.Duration("handle-delay", 0, "delay duration for handling each request")
	shutdownTimeout := flag.Duration("shutdown-timeout", 5*time.Second, "shutdown timeout")
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
		if *handleDelay > 0 {
			time.Sleep(*handleDelay)
		}
		fmt.Fprintf(w, "response from pid %d.\n", os.Getpid())
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

	if *startDelay > 0 {
		time.Sleep(*startDelay)
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
