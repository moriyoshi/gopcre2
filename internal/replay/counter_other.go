//go:build !linux

package replay

import "fmt"

type counter struct{}

func openCounter() (*counter, error) { return nil, fmt.Errorf("instruction counter requires Linux") }
func (*counter) start()              {}
func (*counter) stop() uint64        { return 0 }
func (*counter) close()              {}
