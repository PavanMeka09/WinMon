//go:build amd64

package audio

//go:noescape
func syscallVolume(fn, this uintptr, volume float32, eventContext uintptr) uintptr
