package main

import (
	"errors"
	"os"

	"github.com/bskyn/peek/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		var exitCoder interface{ ExitCode() int }
		if errors.As(err, &exitCoder) {
			os.Exit(exitCoder.ExitCode())
		}
		os.Exit(1)
	}
}
