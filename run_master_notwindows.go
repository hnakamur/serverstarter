// +build !windows

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
	"time"
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

	childCmd, err := s.startProcess()
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
			newChildCmd, err := s.startProcess()
			if err != nil {
				return fmt.Errorf("error in RunMaster after starting new worker; %v", err)
			}

			err = s.waitReady()
			if err != nil {
				return fmt.Errorf("error in RunMaster after waiting ready; %v", err)
			}

			childPID := childCmd.Process.Pid
			err = syscall.Kill(childPID, s.gracefulShutdownSignalToChild)
			if err != nil {
				return fmt.Errorf("error in RunMaster after sending signal %q to worker pid=%d after receiving SIGHUP; %v", s.gracefulShutdownSignalToChild, childPID, err)
			}

			doneC := make(chan error)
			go func() {
				doneC <- childCmd.Wait()
			}()

			timer := time.NewTimer(s.childShutdownWaitTimeout)
			defer timer.Stop()

			select {
			case err := <-doneC:
				if err != nil {
					// NOTE: We do NOT return the error here, since we want to
					// move forward and make the mater process continue running.
					fmt.Fprintf(os.Stderr, "error in waiting for child to graceful shutdown: %+v", err)
				}
			case <-timer.C:
				err = syscall.Kill(childPID, syscall.SIGKILL)
				if err != nil {
					return fmt.Errorf("error in RunMaster after sending signal SIGKILL to worker pid=%d after receiving SIGHUP: %+v", childPID, err)
				}

				err = <-doneC
				if err != nil {
					// NOTE: We do NOT return the error here, since we want to
					// move forward and make the mater process continue running.
					fmt.Fprintf(os.Stderr, "error in waiting for child to be killed: %+v", err)
				}
			}

			childCmd = newChildCmd

		case syscall.SIGINT, syscall.SIGTERM:
			childPID := childCmd.Process.Pid
			err = syscall.Kill(childPID, syscall.SIGTERM)
			if err != nil {
				return fmt.Errorf("error in RunMaster after sending SIGTERM to worker pid=%d after receiving %v; %v", childPID, sig, err)
			}
			return nil
		}
	}
}

func (s *Starter) startProcess() (cmd *exec.Cmd, err error) {
	// This code is based on
	// https://github.com/facebookgo/grace/blob/4afe952a37a495ae4ac0c1d4ce5f66e91058d149/gracenet/net.go#L201-L248
	// https://github.com/cloudflare/tableflip/blob/78281f93d0754df1263259949d2468c5d0376dc6/child.go#L20-L76

	// These pipes are used for communication between parent and child
	// readyW is passed to the child, readyR stays with the parent
	readyR, readyW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("pipe failed in startProcess; %v", err)
	}
	s.readyPipeR = readyR

	type filer interface {
		File() (*os.File, error)
	}

	files := make([]*os.File, 1+len(s.listeners))
	files[0] = readyW
	for i, l := range s.listeners {
		f, err := l.(filer).File()
		if err != nil {
			return nil, fmt.Errorf("error in startProcess after getting file from listener; %v", err)
		}
		files[1+i] = f
		defer files[1+i].Close()
	}

	// Use the original binary location. This works with symlinks such that if
	// the file it points to has been changed we will use the updated symlink.
	argv0, err := exec.LookPath(os.Args[0])
	if err != nil {
		return nil, fmt.Errorf("error in startProcess after looking path of the original binary location; %v", err)
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

	cmd = exec.Command(argv0, os.Args[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = files
	err = cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("error in startProcess after starting worker process; %v", err)
	}

	// NOTE: This is needed to avoid pipe fd leak.
	readyW.Close()

	return cmd, nil
}
