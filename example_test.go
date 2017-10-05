package serverstarter_test

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"

	"github.com/hnakamur/contextify"
	"github.com/hnakamur/serverstarter"
)

func Example() {
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

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)

		s := <-c
		log.Printf("received signal, %s", s)
		cancel()
		log.Printf("cancelled context")
	}()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "from pid %d.\n", os.Getpid())
	})
	srv := http.Server{}
	run := contextify.Contextify(func() error {
		return srv.Serve(l)
	}, func() error {
		return srv.Shutdown(context.Background())
	}, nil)
	err := run(ctx)
	if err != nil {
		log.Printf("got error, %v", err)
	}
	log.Print("exiting")
}
