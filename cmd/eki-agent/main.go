package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	showVersion := flag.Bool("version", false, "print the development version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Fprintln(os.Stdout, "eki-agent dev")
		return
	}

	fmt.Fprintln(os.Stdout, "eki-agent: M0 skeleton; no worker session configured")
}
