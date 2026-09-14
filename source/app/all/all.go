// Package all blank-imports every registry backend so that their package
// init functions run and register themselves with the source registry.
//
// This file is the single registration point for backends, replacing the code
// generator that used to produce these imports at build time. To add a new
// backend, create its package under source/app/<name> and add a matching blank
// import below.
package all

import (
	_ "github.com/ZHallen122/RegTool/source/app/gem"
	_ "github.com/ZHallen122/RegTool/source/app/homebrew"
	_ "github.com/ZHallen122/RegTool/source/app/npm"
	_ "github.com/ZHallen122/RegTool/source/app/pip"
	_ "github.com/ZHallen122/RegTool/source/app/yarn"
)
