package main

import (
	"fmt"
	"os"

	"github.com/ZHallen122/RegTool/internal/tui"
	_ "github.com/ZHallen122/RegTool/source/app/all"
)

func main() {
	if err := tui.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "regtool:", err)
		os.Exit(1)
	}
}
