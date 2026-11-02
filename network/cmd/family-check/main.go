// family-check is a disposable compatibility probe, not a chain daemon.
package main

import (
	"fmt"
	"os"

	"github.com/maxwell2010/go-smart-node/network/internal/family"
)

func main() {
	home, err := os.MkdirTemp("", "mintcandy-family-probe-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(home)
	if err := family.CheckWiring(home); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Owned Cosmos EVM application wiring initialized in memory. Full native feature parity and guarded daemon validation remain pending.")
}
