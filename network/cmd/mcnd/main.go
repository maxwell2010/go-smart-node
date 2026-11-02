package main

import (
	"fmt"
	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"
	"os"
)

func main() {
	root := NewRootCmd()
	if err := svrcmd.Execute(root, "MCND", defaultNodeHome()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
