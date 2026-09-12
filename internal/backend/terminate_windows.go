//go:build windows

package backend

import "os"

// Windows child app-server processes are owned by the router. Go cannot send
// os.Interrupt to an arbitrary Windows child reliably, so terminate the owned
// process directly during router shutdown.
func terminateProcess(process *os.Process) error {
	return process.Kill()
}
