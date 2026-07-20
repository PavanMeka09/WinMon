//go:build !amd64

package audio

import (
	"math"
	"syscall"
)

func syscallVolume(fn, this uintptr, volume float32, eventContext uintptr) uintptr {
	ret, _, _ := syscall.SyscallN(fn, this, uintptr(math.Float32bits(volume)), eventContext)
	return ret
}
