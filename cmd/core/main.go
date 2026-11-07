package main

import (
	"os"

	"github.com/CharitonMedia/togavsu/internal/bootstrap"
)

func main() {
	os.Exit(bootstrap.Run(os.Args, os.Stdout, os.Stderr))
}
