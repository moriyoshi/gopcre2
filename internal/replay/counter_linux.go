package replay

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

type counter struct{ fd int }

func openCounter() (*counter, error) {
	var number uintptr
	switch runtime.GOARCH {
	case "arm64":
		number = 241
	case "amd64":
		number = 298
	default:
		return nil, fmt.Errorf("unsupported counter architecture")
	}
	var attr [128]byte
	binary.LittleEndian.PutUint32(attr[4:], 128)
	binary.LittleEndian.PutUint64(attr[8:], 1)
	binary.LittleEndian.PutUint64(attr[40:], 1|1<<5|1<<6)
	fd, _, errno := syscall.Syscall6(number, uintptr(unsafe.Pointer(&attr[0])), 0, ^uintptr(0), ^uintptr(0), 0, 0)
	if errno != 0 {
		return nil, errno
	}
	return &counter{int(fd)}, nil
}
func (c *counter) start() {
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(c.fd), 0x2403, 0)
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(c.fd), 0x2400, 0)
}
func (c *counter) stop() uint64 {
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(c.fd), 0x2401, 0)
	var b [8]byte
	syscall.Read(c.fd, b[:])
	return binary.LittleEndian.Uint64(b[:])
}
func (c *counter) close() { syscall.Close(c.fd) }
