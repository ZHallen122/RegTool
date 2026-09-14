// Package all blank-imports every registry backend so that their package
// init functions run and register themselves with the source registry.
//
// This file is the single registration point for backends, replacing the code
// generator that used to produce these imports at build time. To add a new
// backend, create its package under source/app/<name> and add a matching blank
// import below.
package all

import (
	_ "regtool/source/app/gem"
	_ "regtool/source/app/homebrew"
	_ "regtool/source/app/npm"
	_ "regtool/source/app/pip"
	_ "regtool/source/app/yarn"
)
