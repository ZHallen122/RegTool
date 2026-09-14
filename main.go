package main

import (
	"os"

	"github.com/ZHallen122/RegTool/internal/cli"
	_ "github.com/ZHallen122/RegTool/source/app/all"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
