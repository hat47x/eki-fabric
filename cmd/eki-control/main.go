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
		fmt.Fprintln(os.Stdout, "eki-control dev")
		return
	}

	fmt.Fprintln(os.Stdout, "eki-control: M0 skeleton; no listeners configured")
}
