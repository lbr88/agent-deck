package main

import (
	"fmt"
	"os"
)

func warnPostCommitError(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: post-save sync failed: %v\n", err)
	}
}
