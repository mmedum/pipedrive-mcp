//go:build !live

// Command evals drives a model through this server's tools and scores
// what it did. It is behind a build tag so an ordinary `go build ./...`
// never tries to reach the network, a CRM, or a model.
//
// The task table itself is NOT behind the tag: `go test ./scripts/evals`
// walks every prompt without credentials, which is where the
// unsubstituted-placeholder guard belongs.
package main

import "fmt"

func main() {
	fmt.Println("evals needs credentials, a network and the claude CLI, so it is behind a build tag.\n" +
		"Run it with `make evals`, or `go run -tags=live ./scripts/evals -bin ./pipedrive-mcp`.")
}
