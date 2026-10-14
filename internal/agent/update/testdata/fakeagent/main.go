// Command fakeagent is a stand-in for trica-agent, built by the tests with
// -ldflags "-X main.version=...". What it does is chosen by TRICA_TEST_MODE:
// "good" confirms its version like a healthy agent, "crash" exits at once,
// "hang" never reports.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/VargaFoundation/trica/internal/agent/update"
)

var version = "0.0.0"

func main() {
	switch os.Getenv("TRICA_TEST_MODE") {
	case "crash":
		os.Exit(1)
	case "hang":
		time.Sleep(time.Hour)
	default:
		if _, err := update.NewStore(os.Args[1]).Confirm(version); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("ok", version)
	}
}
