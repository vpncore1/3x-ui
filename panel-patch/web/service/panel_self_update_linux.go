//go:build linux

package service

import "syscall"

func sysProcAttrDetached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid: true,
	}
}
