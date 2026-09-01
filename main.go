package main

import (
	"flag"
	"github.com/timoffmax/social-link-bot/app"
	"github.com/timoffmax/social-link-bot/services"
	"log"
)

func main() {
	// Command line flags
	testLinks := flag.Bool("test-links", false, "Run real link tests for all platforms")
	quickTest := flag.Bool("quick-test", false, "Run a quick test with one reliable link")
	flag.Parse()

	if *testLinks {
		log.Printf("🧪 Running comprehensive real link tests...")
		tester := services.NewRealLinkTester()
		tester.RunTests()
		return
	}

	if *quickTest {
		log.Printf("🚀 Running quick test...")
		tester := services.NewRealLinkTester()
		tester.RunQuickTest()
		return
	}

	// Normal bot operation
	app.Run()
}
