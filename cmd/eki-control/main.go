// Command eki-control is the Control Plane process.
//
// This file only parses flags, wires configuration, and manages the process
// lifecycle. Admission rules and Authority logic live in internal packages.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hat47x/eki-fabric/internal/control/clientapi"
	"github.com/hat47x/eki-fabric/internal/control/service"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "gen-token" {
		return genToken(stdout, stderr)
	}

	fs := flag.NewFlagSet("eki-control", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print the development version and exit")
	configPath := fs.String("config", "", "path to the control plane configuration file; without it the process starts nothing")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, "eki-control dev")
		return 0
	}
	if *configPath == "" {
		fmt.Fprintln(stdout, "eki-control: M0 skeleton; no listeners configured (pass --config to serve the Client API)")
		return 0
	}

	cfg, err := service.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "eki-control: %v\n", err)
		return 2
	}

	log := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := service.Run(ctx, cfg, log); err != nil {
		log.Error("control plane stopped with an error", "error", err.Error())
		return 1
	}
	return 0
}

// genToken prints a new random bearer token and the hash to put in the
// configuration. The token is shown once; only the hash is stored.
func genToken(stdout, stderr io.Writer) int {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		fmt.Fprintf(stderr, "eki-control: generate token: %v\n", err)
		return 1
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	fmt.Fprintf(stdout, "token:  %s\n", token)
	fmt.Fprintf(stdout, "sha256: %s\n", clientapi.HashToken(token))
	fmt.Fprintln(stderr, "Give the token to the client now. Put only the sha256 value in the configuration; the token cannot be recovered from it.")
	return 0
}
