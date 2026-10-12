//go:build !unix

package main

import "os/exec"

func detachProcessGroup(*exec.Cmd) {}
