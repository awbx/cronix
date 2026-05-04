package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/awbx/cronix/go/internal/cli/commands"
)

// exitCoder is implemented by errors that carry a specific exit code
// (e.g. drift --exit-on-drift returns code 5).
type exitCoder interface {
	error
	ExitCode() int
}

func main() {
	if err := commands.NewRootCmd().Execute(); err != nil {
		var ec exitCoder
		if errors.As(err, &ec) {
			fmt.Fprintln(os.Stderr, ec.Error())
			os.Exit(ec.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
