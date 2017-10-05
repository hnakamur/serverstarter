// Package serverstarter provides a server starter which can be used to do graceful restart.
package serverstarter

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
)

const (
	stdFdCount          = 3 // stdin, stdout, stderr
	defaultEnvListenFDs = "LISTEN_FDS"
)

// Starter is a server starter.
type Starter struct {
	envListenFDs                  string
	workingDirectory              string
	listeners                     []net.Listener
	gracefulShutdownSignalToChild syscall.Signal
}

// Option is the type for configuring a Starter.
type Option func(s *Starter)

// New returns a new Starter.
func New(options ...Option) *Starter {
	s := &Starter{
		envListenFDs:                  defaultEnvListenFDs,
		gracefulShutdownSignalToChild: syscall.SIGTERM,
	}
	for _, o := range options {
		o(s)
	}
	return s
}

// SetEnvName sets the environment variable name for passing the listener file descriptor count to the worker process.
// When this options is not called, the environment variable name will be "LISTEN_FDS".
func SetEnvName(name string) Option {
	return func(s *Starter) {
		s.envListenFDs = name
	}
}

// SetGracefulShutdownSignalToChild sets the signal to send to child for graceful shutdown.
// If no SetGracefulShutdownSignalToChild is called, the default value is syscall.SIGTERM.
func SetGracefulShutdownSignalToChild(sig syscall.Signal) Option {
	return func(s *Starter) {
		s.gracefulShutdownSignalToChild = sig
	}
}

// IsMaster returns whether this process is the master or not.
// It returns true if this process is the master, and returns false if this process is the worker.
func (s *Starter) IsMaster() bool {
	_, isWorker := os.LookupEnv(s.envListenFDs)
	return !isWorker
}

// Listeners returns the listeners passed from the master if this is called by the worker process.
// It returns nil when this is called by the master process.
func (s *Starter) Listeners() ([]net.Listener, error) {
	countStr, isWorker := os.LookupEnv(s.envListenFDs)
	if !isWorker {
		return nil, nil
	}

	count, err := strconv.Atoi(countStr)
	if err != nil {
		return nil, fmt.Errorf("error in Listeners after getting invalid listener count; %v", err)
	}
	listeners := make([]net.Listener, count)
	for i := 0; i < count; i++ {
		fd := uintptr(i + stdFdCount)
		file := os.NewFile(fd, "listener")
		l, err := net.FileListener(file)
		if err != nil {
			return nil, fmt.Errorf("error in Listeners after failing to create listener; %v", err)
		}
		listeners[i] = l
	}
	return listeners, nil
}
