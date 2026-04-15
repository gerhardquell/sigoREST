// List available models example
//
// This example shows how to get information about available models.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/gquell/sigoclient"
)

func main() {
	client := sigoclient.New("http://127.0.0.1:9080")
	ctx := context.Background()

	if !client.Ping(ctx) {
		log.Fatal("❌ sigoREST server is not responding")
	}

	fmt.Println("📋 Available Models\n")

	// Get health info
	health, err := client.Health(ctx)
	if err != nil {
		log.Fatalf("❌ Error: %v", err)
	}
	fmt.Printf("Server Status: %s\n", health.Status)
	fmt.Printf("Available Models: %d\n", health.AvailableModels)
	fmt.Printf("Memory Set: %v\n", health.MemorySet)
	fmt.Println()

	// List all models
	models, err := client.ListModels(ctx)
	if err != nil {
		log.Fatalf("❌ Error: %v", err)
	}

	// Group by provider
	providers := make(map[string][]sigoclient.ModelInfo)
	for _, model := range models {
		provider := "Other"
		switch {
		case strings.Contains(model.Endpoint, "mammouth"):
			provider = "Mammoth.ai"
		case strings.Contains(model.Endpoint, "moonshot"):
			provider = "Moonshot"
		case strings.Contains(model.Endpoint, "z.ai"):
			provider = "Z.ai"
		case strings.HasPrefix(model.Shortcode, "ollama-"):
			provider = "Ollama (Local)"
		}
		providers[provider] = append(providers[provider], model)
	}

	for provider, modelList := range providers {
		fmt.Printf("\n--- %s ---\n", provider)
		fmt.Printf("%-30s %-15s %8s %8s\n", "ID", "Shortcode", "Input", "Output")
		fmt.Println(strings.Repeat("-", 65))
		for _, m := range modelList {
			fmt.Printf("%-30s %-15s $%6.2f $%6.2f\n",
				m.ID, m.Shortcode, m.InputCost, m.OutputCost)
		}
	}
}
