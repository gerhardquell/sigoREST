// Session-based conversation example
//
// This example shows how to use sessions to maintain conversation context.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/gquell/sigoclient"
)

func main() {
	client := sigoclient.New("http://127.0.0.1:9080")
	ctx := context.Background()

	if !client.Ping(ctx) {
		log.Fatal("❌ sigoREST server is not responding")
	}

	sessionID := "go-example-session"
	model := "kimi"

	fmt.Println("💡 Session-based conversation example")
	fmt.Printf("   Session: %s\n", sessionID)
	fmt.Printf("   Model: %s\n", model)
	fmt.Println()

	// First message
	fmt.Println("👤 User: My name is Bob and I love Go programming.")
	resp, err := client.Chat(ctx, model, "My name is Bob and I love Go programming.",
		sigoclient.WithSession(sessionID),
	)
	if err != nil {
		log.Fatalf("❌ Error: %v", err)
	}
	fmt.Printf("🤖 Assistant: %s\n\n", resp.Content)

	// Second message - context is preserved via session
	fmt.Println("👤 User: What's my name and what do I like?")
	resp, err = client.Chat(ctx, model, "What's my name and what do I like?",
		sigoclient.WithSession(sessionID),
	)
	if err != nil {
		log.Fatalf("❌ Error: %v", err)
	}
	fmt.Printf("🤖 Assistant: %s\n\n", resp.Content)

	// Third message with system prompt
	fmt.Println("👤 User: Can you recommend a Go library for HTTP clients?")
	resp, err = client.Chat(ctx, model, "Can you recommend a Go library for HTTP clients?",
		sigoclient.WithSession(sessionID),
		sigoclient.WithSystemPrompt("You are a helpful Go expert. Be concise."),
	)
	if err != nil {
		log.Fatalf("❌ Error: %v", err)
	}
	fmt.Printf("🤖 Assistant: %s\n", resp.Content)
}
