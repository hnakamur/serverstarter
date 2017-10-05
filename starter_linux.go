package serverstarter

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

// RunMaster starts a worker process and run the loop for starting and stopping the worker
// on signals.
//
// If the master process receives a SIGHUP, it starts a new worker and stop the old worker
// by sending a signal set by SetGracefulShutdownSignalToChild.
// If the master process receives a SIGINT or a SIGTERM, it sends the SIGTERM to the worker
// and exists.
func (s *Starter) RunMaster(listeners ...net.Listener) error {
	s.listeners = listeners
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("error in RunMaster after failing to get working directory; %v", err)
	}
	s.workingDirectory = wd

	childPID, err := s.startProcess()
	if err != nil {
		return fmt.Errorf("error in RunMaster after starting worker; %v", err)
	}

	signals := make(chan os.Signal, 1)
	// NOTE: The signals SIGKILL and SIGSTOP may not be caught by a program.
	// https://golang.org/pkg/os/signal/#hdr-Types_of_signals
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	for {
		sig := <-signals
		switch sig {
		case syscall.SIGHUP:
			newChildPid, err := s.startProcess()
			if err != nil {
				return fmt.Errorf("error in RunMaster after starting new worker; %v", err)
			}

			err = syscall.Kill(childPID, s.gracefulShutdownSignalToChild)
			if err != nil {
				return fmt.Errorf("error in RunMaster after sending signal %q to worker pid=%d after receiving SIGHUP; %v", s.gracefulShutdownSignalToChild, childPID, err)
			}

			_, err = syscall.Wait4(childPID, nil, 0, nil)
			if err != nil {
				return fmt.Errorf("error in RunMaster after waiting worker pid=%d; %v", childPID, err)
			}

			childPID = newChildPid

		case syscall.SIGINT, syscall.SIGTERM:
			err := syscall.Kill(childPID, syscall.SIGTERM)
			if err != nil {
				return fmt.Errorf("error in RunMaster after sending SIGTERM to worker pid=%d after receiving %v; %v", childPID, sig, err)
			}
			return nil
		}
	}
}

func (s *Starter) startProcess() (pid int, err error) {
	// This code is based on
	// https://github.com/facebookgo/grace/blob/4afe952a37a495ae4ac0c1d4ce5f66e91058d149/gracenet/net.go#L201-L248

	type filer interface {
		File() (*os.File, error)
	}

	files := make([]*os.File, len(s.listeners))
	for i, l := range s.listeners {
		f, err := l.(filer).File()
		if err != nil {
			return 0, fmt.Errorf("error in startProcess after getting file from listener; %v", err)
		}
		files[i] = f
		defer files[i].Close()
	}

	// Use the original binary location. This works with symlinks such that if
	// the file it points to has been changed we will use the updated symlink.
	argv0, err := exec.LookPath(os.Args[0])
	if err != nil {
		return 0, fmt.Errorf("error in startProcess after looking path of the original binary location; %v", err)
	}

	// Pass on the environment and replace the old count key with the new one.
	envListenFDsPrefix := s.envListenFDs + "="
	var env []string
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, envListenFDsPrefix) {
			env = append(env, v)
		}
	}
	envFDs := strconv.AppendInt([]byte(envListenFDsPrefix), int64(len(s.listeners)), 10)
	env = append(env, string(envFDs))

	allFiles := append([]*os.File{os.Stdin, os.Stdout, os.Stderr}, files...)
	process, err := os.StartProcess(argv0, os.Args, &os.ProcAttr{
		Dir:   s.workingDirectory,
		Env:   env,
		Files: allFiles,
	})
	if err != nil {
		return 0, fmt.Errorf("error in startProcess after starting worker process; %v", err)
	}
	return process.Pid, nil
}
