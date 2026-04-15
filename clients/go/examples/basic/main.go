// Basic chat example with sigoclient
//
// This example shows how to send a simple chat message to sigoREST.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gquell/sigoclient"
)

func main() {
	// Create client with timeout
	client := sigoclient.New("http://127.0.0.1:9080",
		sigoclient.WithTimeout(30*time.Second),
	)

	// Check if server is alive
	ctx := context.Background()
	if !client.Ping(ctx) {
		log.Fatal("❌ sigoREST server is not responding")
	}
	fmt.Println("✅ Connected to sigoREST")
	fmt.Println()

	// Simple chat
	resp, err := client.Chat(ctx, "kimi", "Explain quantum computing in one sentence.")
	if err != nil {
		if sigoErr, ok := sigoclient.IsError(err); ok {
			log.Fatalf("❌ API Error %d: %s", sigoErr.StatusCode, sigoErr.Message)
		}
		log.Fatalf("❌ Error: %v", err)
	}

	fmt.Printf("🤖 Model: %s\n", resp.Model)
	fmt.Printf("💬 Response: %s\n", resp.Content)
}
