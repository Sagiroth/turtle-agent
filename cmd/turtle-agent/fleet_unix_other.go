//go:build unix && !linux

package main

import "syscall"

// dieWithParent: no parent-death signal outside Linux.
func dieWithParent(*syscall.SysProcAttr) {}
