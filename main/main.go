// Copyright (C) 2019-2021, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package main

import (
	"fmt"
	"os"

	"github.com/spf13/afero"

	"github.com/shubhamdubey02/apm/cmd"
)

func main() {
	apm, err := cmd.New(afero.NewOsFs())
	if err != nil {
		fmt.Printf("Failed to initialize the apm command: %s.\n", err)
		os.Exit(1)
	}

	if err := apm.Execute(); err != nil {
		fmt.Printf("Unexpected error %s.\n", err)
		os.Exit(1)
	}
}
