// An example HTTP/1.1 and HTTP/2.0 server which supports graceful restart
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/hnakamur/serverstarter"
)

func main() {
	httpAddr := flag.String("http", ":8080", "HTTP listen address")
	httpsAddr := flag.String("https", ":8443", "HTTPS listen address")
	pidFile := flag.String("pidfile", "graceserver.pid", "pid file")
	handleDelay := flag.Duration("handle-delay", 0, "delay duration for handling each request")
	fdEnvName := flag.String("fdenv", "LISTEN_FDS", "environment variable for passing file discriptor count to worker")
	startDelay := flag.Duration("start-delay", 0, "delay duration before start accepting requests")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if *httpAddr == "" && *httpsAddr == "" {
		log.Fatal("you must specify http and/or https listen addresses")
	}

	var listeners []net.Listener
	var httpLn, httpsLn net.Listener
	var err error
	pid := os.Getpid()
	starter := serverstarter.New(serverstarter.SetEnvName(*fdEnvName))
	if starter.IsMaster() {
		log.Printf("master pid=%d started.", pid)
		if *pidFile != "" {
			data := strconv.AppendInt(nil, int64(pid), 10)
			err = ioutil.WriteFile(*pidFile, data, 0666)
			if err != nil {
				log.Fatalf("failed to write pid file; %v", err)
			}
		}
		if *httpAddr != "" {
			httpLn, err = net.Listen("tcp", *httpAddr)
			if err != nil {
				log.Fatalf("failed to listen http %s, pid=%d, err=%v", *httpAddr, pid, err)
			}
			listeners = append(listeners, httpLn)
		}
		if *httpsAddr != "" {
			httpsLn, err = net.Listen("tcp", *httpsAddr)
			if err != nil {
				log.Fatalf("failed to listen https %s, pid=%d, err=%v", *httpsAddr, pid, err)
			}
			listeners = append(listeners, httpsLn)
		}

		err = starter.RunMaster(listeners...)
		if err != nil {
			log.Fatalf("failed to run master, pid=%d, err=%v", pid, err)
		}
		return
	}

	log.Printf("worker pid=%d started.", pid)
	listeners, err = starter.Listeners()
	if err != nil {
		log.Fatalf("failed to get listeners, pid=%d, err=%v", pid, err)
	}
	i := 0
	if *httpAddr != "" {
		httpLn = listeners[i]
		i++
	}
	if *httpsAddr != "" {
		httpsLn = listeners[i]
		i++
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if *handleDelay > 0 {
			time.Sleep(*handleDelay)
			fmt.Fprintf(w, "from pid %d after %s delay.\n", os.Getpid(), *handleDelay)
		} else {
			fmt.Fprintf(w, "from pid %d.\n", os.Getpid())
		}
	})

	var tlsConfig *tls.Config
	if httpsLn != nil {
		cert, err := generateSelfSignedCertificate()
		if err != nil {
			log.Fatalf("failed to generate self signed certificate; %v", err)
		}
		tlsConfig := &tls.Config{
			NextProtos:   []string{"h2"},
			Certificates: []tls.Certificate{cert},
		}
		httpsLn = tls.NewListener(httpsLn, tlsConfig)
	}
	srv := &http.Server{
		TLSConfig: tlsConfig,
	}

	idleConnsClosed := make(chan struct{})
	go func() {
		sigterm := make(chan os.Signal, 1)
		signal.Notify(sigterm, syscall.SIGTERM)
		<-sigterm

		srv.SetKeepAlivesEnabled(false)
		if err := srv.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			log.Printf("http(s) server Shutdown: %v", err)
		}
		close(idleConnsClosed)
	}()

	if *startDelay > 0 {
		time.Sleep(*startDelay)
	}

	var wgStart, wgStop sync.WaitGroup
	if httpLn != nil {
		wgStart.Add(1)
		wgStop.Add(1)
		go func() {
			defer wgStop.Done()
			wgStart.Done()
			log.Printf("worker pid=%d http server start Serve", pid)
			if err := srv.Serve(httpLn); err != http.ErrServerClosed {
				// Error starting or closing listener:
				log.Printf("http server Serve: %v", err)
			}
		}()
	}
	if httpsLn != nil {
		wgStart.Add(1)
		wgStop.Add(1)
		go func() {
			defer wgStop.Done()
			wgStart.Done()
			log.Printf("worker pid=%d https server start Serve", pid)
			if err := srv.Serve(httpsLn); err != http.ErrServerClosed {
				// Error starting or closing listener:
				log.Printf("https server Serve: %v", err)
			}
		}()
	}

	wgStart.Wait()
	if err := starter.SendReady(); err != nil {
		log.Printf("failed to send ready: %v", err)
	}

	wgStop.Wait()
	<-idleConnsClosed
	log.Printf("worker pid=%d exiting run func", pid)
}

func generateSelfSignedCertificate() (tls.Certificate, error) {
	privatekey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	publickey := &privatekey.PublicKey

	now := time.Now()
	template := &x509.Certificate{
		IsCA: true,
		BasicConstraintsValid: true,
		SubjectKeyId:          nil,
		SerialNumber:          big.NewInt(1),
		Subject: pkix.Name{
			Country:      []string{"jp"},
			Organization: []string{"example organization"},
		},
		NotBefore:   now,
		NotAfter:    now.AddDate(0, 0, 30),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	parent := template
	cert, err := x509.CreateCertificate(rand.Reader, template, parent, publickey, privatekey)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.Certificate{
		Certificate: [][]byte{cert},
		PrivateKey:  privatekey,
	}, nil
}
