package main

import (
	"fmt"
	"os"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	subject := ""
	if len(os.Args) == 2 {
		subject = strings.TrimSpace(os.Args[1])
	}
	if !strings.HasPrefix(subject, "https://") && !strings.HasPrefix(subject, "mailto:") {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/webpush-keygen https://chat.example.com")
		os.Exit(2)
	}
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate VAPID keys failed")
		os.Exit(1)
	}
	fmt.Printf("IM_WEB_PUSH_PUBLIC_KEY=%s\n", publicKey)
	fmt.Printf("IM_WEB_PUSH_PRIVATE_KEY=%s\n", privateKey)
	fmt.Printf("IM_WEB_PUSH_SUBJECT=%s\n", subject)
}
