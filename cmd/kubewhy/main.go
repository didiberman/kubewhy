package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/didiberman/kubewhy/internal/agent"
	"github.com/didiberman/kubewhy/internal/dashboard"
)

// version is set at build time via -ldflags "-X main.version=..." (see .goreleaser.yaml).
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "watch" {
		runWatch(os.Args[2:])
		return
	}
	runAsk()
}

func runAsk() {
	model := flag.String("model", agent.DefaultModel, "Any model slug available on OpenRouter.")
	showVersion := flag.Bool("version", false, "Print the kubewhy version and exit.")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: kubewhy [--model slug] \"question about your cluster\"\n       kubewhy watch [--namespace ns] [--interval 5s]\n\nkubewhy is a read-only Kubernetes investigator. It never mutates your cluster.\n\nFlags:\n")
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

	if _, err := agent.Investigate(context.Background(), question, apiKey, *model, agent.ConsoleReporter{}); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	model := fs.String("model", agent.DefaultModel, "Any model slug available on OpenRouter.")
	namespace := fs.String("namespace", "", "Limit to one namespace (default: all namespaces).")
	interval := fs.Duration("interval", 5*time.Second, "How often to re-check cluster health.")
	fs.Parse(args)

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "OPENROUTER_API_KEY is not set.")
		os.Exit(1)
	}

	cfg := dashboard.Config{
		APIKey:    apiKey,
		Model:     *model,
		Namespace: *namespace,
		Interval:  *interval,
	}
	if err := dashboard.Run(context.Background(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
