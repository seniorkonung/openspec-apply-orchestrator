//go:build paseo_integration

package main

import (
	"fmt"
	"os"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/testpaseo"
)

func main() {
	if err := testpaseo.RunProvider(os.Stdin, os.Stdout, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
