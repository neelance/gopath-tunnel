package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/websocket"

	"github.com/neelance/gopath-tunnel/client"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: gopath-tunnel [url] [packages]")
		os.Exit(1)
	}
	url := os.Args[1]
	patterns := os.Args[2:]

	gotError := false
	h := client.NewHandler(patterns, &gotError)

	for {
		fmt.Printf("Connecting to %s...\n", url)

		ws, err := websocket.Dial(url, "", "http://localhost/")
		if err != nil {
			fmt.Printf("Error: %s\n\n", err)
			time.Sleep(2 * time.Second)
			continue
		}

		fmt.Println("Connected.")

		s := &http2.Server{}
		s.ServeConn(ws, &http2.ServeConnOpts{Handler: h})
		if gotError {
			os.Exit(1)
		}
		fmt.Printf("Connection lost.\n\n")

		time.Sleep(2 * time.Second)
	}
}
