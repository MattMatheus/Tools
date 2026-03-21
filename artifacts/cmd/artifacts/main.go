package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"artifactmount/internal/app"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to config JSON")
	flag.Parse()

	if configPath == "" {
		fmt.Fprintln(os.Stderr, "--config is required")
		os.Exit(2)
	}

	exitCode, err := app.Run(context.Background(), configPath, flag.Args(), os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(exitCode)
}
