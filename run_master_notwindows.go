//go:build !windows

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
	childWaitErrC := make(chan error, 1)
	go waitChild(childCmd, childWaitErrC)
	fmt.Printf("started initial worker: pid=%d\n", childCmd.Process.Pid)

	if err := s.waitReady(); err != nil {
		return fmt.Errorf("error in RunMaster after waiting ready from initial worker; %v", err)
	}
	fmt.Println("received ready from initial worker")

	signals := make(chan os.Signal, 1)
	// NOTE: The signals SIGKILL and SIGSTOP may not be caught by a program.
	// https://golang.org/pkg/os/signal/#hdr-Types_of_signals
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	for {
		select {
		case sig := <-signals:
			switch sig {
			case syscall.SIGHUP:
				newChildCmd, err := s.startProcess()
				if err != nil {
					return fmt.Errorf("error in RunMaster after starting new worker; %v", err)
				}
				// Recreate error channel to ignore error from old child.
				newChildWaitErrC := make(chan error, 1)
				go waitChild(newChildCmd, newChildWaitErrC)
				fmt.Printf("started new worker: pid=%d\n", newChildCmd.Process.Pid)

				if err := s.waitReady(); err != nil {
					return fmt.Errorf("error in RunMaster after waiting ready; %v", err)
				}
				fmt.Println("received ready from new worker")

				oldChildPID := childCmd.Process.Pid
				if err := syscall.Kill(oldChildPID, s.gracefulShutdownSignalToChild); err != nil {
					return fmt.Errorf("error in RunMaster after sending signal %q to worker pid=%d after receiving SIGHUP; %v", s.gracefulShutdownSignalToChild, oldChildPID, err)
				}

				timer := time.NewTimer(s.childShutdownWaitTimeout)
				select {
				case err := <-childWaitErrC:
					timer.Stop()
					if err != nil {
						// NOTE: We do NOT return the error here, since we want to
						// move forward and make the mater process continue running.
						fmt.Fprintf(os.Stderr, "error in waiting for child to graceful shutdown: %+v\n", err)
					}
				case <-timer.C:
					if err := syscall.Kill(oldChildPID, syscall.SIGKILL); err != nil {
						return fmt.Errorf("error in RunMaster after sending signal SIGKILL to worker pid=%d after receiving SIGHUP: %+v", oldChildPID, err)
					}

					if err := <-childWaitErrC; err != nil {
						// NOTE: We do NOT return the error here, since we want to
						// move forward and make the mater process continue running.
						fmt.Fprintf(os.Stderr, "error in waiting for child to be killed: %+v\n", err)
					}
				}

				childCmd = newChildCmd
				childWaitErrC = newChildWaitErrC

			case syscall.SIGINT, syscall.SIGTERM:
				childPID := childCmd.Process.Pid
				if err := syscall.Kill(childPID, syscall.SIGTERM); err != nil {
					return fmt.Errorf("error in RunMaster after sending SIGTERM to worker pid=%d after receiving %v; %v", childPID, sig, err)
				}
				if err := <-childWaitErrC; err != nil {
					return fmt.Errorf("error from child process: %s", err)
				}
				fmt.Println("stopped child process, exiting.")
				return nil
			}

		case err := <-childWaitErrC:
			if err != nil {
				fmt.Fprintf(os.Stderr, "child process exited err=%v, restarting child.\n", err)
			} else {
				fmt.Println("child process exited without err, restarting child.")
			}
			// always restart child process
			childCmd, err = s.startProcess()
			if err != nil {
				return fmt.Errorf("error in RunMaster after restarting worker; %v", err)
			}
			childWaitErrC = make(chan error, 1)
			go waitChild(childCmd, childWaitErrC)
			fmt.Printf("restarted worker: pid=%d\n", childCmd.Process.Pid)
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

func waitChild(cmd *exec.Cmd, errC chan<- error) {
	errC <- cmd.Wait()
}
