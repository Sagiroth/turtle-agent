//go:build linux

package main

import "syscall"

// dieWithParent: the kernel sends SIGINT to the child when the thread that started it
// exits, which the supervisor keeps alive for its whole run (fleetCmd locks its OS thread).
func dieWithParent(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGINT }
