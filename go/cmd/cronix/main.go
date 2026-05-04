package main

import (
	"fmt"
	"os"

	"github.com/awbx/cronix/go/internal/cli/commands"
)

func main() {
	if err := commands.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
