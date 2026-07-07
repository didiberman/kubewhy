package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/didiberman/kubewhy/internal/agent"
)

// version is set at build time via -ldflags "-X main.version=..." (see .goreleaser.yaml).
var version = "dev"

func main() {
	model := flag.String("model", agent.DefaultModel, "Any model slug available on OpenRouter.")
	showVersion := flag.Bool("version", false, "Print the kubewhy version and exit.")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: kubewhy [--model slug] \"question about your cluster\"\n\nkubewhy is a read-only Kubernetes investigator. It never mutates your cluster.\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println("kubewhy " + version)
		return
	}

	question := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if question == "" {
		flag.Usage()
		os.Exit(2)
	}

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "OPENROUTER_API_KEY is not set.")
		os.Exit(1)
	}

	if _, err := agent.Investigate(context.Background(), question, apiKey, *model); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
