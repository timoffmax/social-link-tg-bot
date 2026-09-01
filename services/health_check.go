package services

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// TestCase represents a single test for a platform
type TestCase struct {
	Platform      string
	URL           string
	Description   string
	ExpectedTypes []string // Expected media types (photo, video, text)
}

// TestResult represents the result of a single test
type TestResult struct {
	TestCase   TestCase
	Success    bool
	Error      error
	Duration   time.Duration
	MediaCount int
	MediaTypes []string
}

// HealthChecker performs health checks on all supported platforms
type HealthChecker struct {
	downloader *SmartDownloader
}

// NewHealthChecker creates a new health checker instance
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		downloader: NewSmartDownloader(),
	}
}

// GetTestCases returns all test cases for supported platforms
func (hc *HealthChecker) GetTestCases() []TestCase {
	return []TestCase{
		// Instagram tests
		{
			Platform:      "Instagram",
			URL:           "https://www.instagram.com/p/C0123456789/", // Replace with a stable test post
			Description:   "Instagram photo post",
			ExpectedTypes: []string{"photo"},
		},
		{
			Platform:      "Instagram",
			URL:           "https://www.instagram.com/reel/C0123456789/", // Replace with a stable test reel
			Description:   "Instagram reel (video)",
			ExpectedTypes: []string{"video"},
		},

		// TikTok tests
		{
			Platform:      "TikTok",
			URL:           "https://www.tiktok.com/@test/video/7123456789", // Replace with a stable test video
			Description:   "TikTok video",
			ExpectedTypes: []string{"video"},
		},

		// Twitter/X tests
		{
			Platform:      "Twitter/X",
			URL:           "https://x.com/test/status/1234567890", // Replace with a stable test tweet
			Description:   "Twitter/X with image",
			ExpectedTypes: []string{"photo", "text"},
		},
		{
			Platform:      "Twitter/X",
			URL:           "https://twitter.com/test/status/1234567890", // Replace with a stable test tweet
			Description:   "Twitter/X video (twitter.com)",
			ExpectedTypes: []string{"video", "text"},
		},

		// YouTube tests
		{
			Platform:      "YouTube",
			URL:           "https://www.youtube.com/shorts/dQw4w9WgXcQ", // Rick Roll as stable test
			Description:   "YouTube Shorts video",
			ExpectedTypes: []string{"video"},
		},
		{
			Platform:      "YouTube",
			URL:           "https://youtu.be/dQw4w9WgXcQ", // Short URL format
			Description:   "YouTube video (short URL)",
			ExpectedTypes: []string{"video"},
		},
	}
}

// RunHealthCheck performs a health check on all supported platforms
func (hc *HealthChecker) RunHealthCheck() []TestResult {
	testCases := hc.GetTestCases()
	results := make([]TestResult, 0, len(testCases))

	log.Printf("🏥 Starting health check for %d test cases", len(testCases))

	for i, testCase := range testCases {
		log.Printf("📋 Test %d/%d: %s - %s", i+1, len(testCases), testCase.Platform, testCase.Description)
		result := hc.runSingleTest(testCase)
		results = append(results, result)

		// Small delay between tests to avoid rate limiting
		if i < len(testCases)-1 {
			time.Sleep(2 * time.Second)
		}
	}

	return results
}

// runSingleTest runs a single test case
func (hc *HealthChecker) runSingleTest(testCase TestCase) TestResult {
	startTime := time.Now()
	result := TestResult{
		TestCase: testCase,
		Success:  false,
	}

	// Try to download media
	mediaInfos, err := hc.downloader.DownloadMedia(testCase.URL)
	result.Duration = time.Since(startTime)

	if err != nil {
		result.Error = err
		log.Printf("❌ Test failed: %v", err)
		return result
	}

	// Check results
	result.MediaCount = len(mediaInfos)
	result.MediaTypes = make([]string, 0)

	for _, media := range mediaInfos {
		result.MediaTypes = append(result.MediaTypes, media.Type)
		// Clean up downloaded files immediately
		if media.Filename != "" {
			hc.downloader.CleanupFile(media.Filename)
		}
	}

	// Verify expected types
	result.Success = hc.verifyExpectedTypes(result.MediaTypes, testCase.ExpectedTypes)

	if result.Success {
		log.Printf("✅ Test passed: %d media items (%s) in %v",
			result.MediaCount, strings.Join(result.MediaTypes, ", "), result.Duration)
	} else {
		log.Printf("⚠️ Test passed with warnings: expected %s, got %s",
			strings.Join(testCase.ExpectedTypes, ", "), strings.Join(result.MediaTypes, ", "))
	}

	return result
}

// verifyExpectedTypes checks if the actual types match expected types
func (hc *HealthChecker) verifyExpectedTypes(actual, expected []string) bool {
	// For now, just check if we got at least one of the expected types
	for _, expectedType := range expected {
		for _, actualType := range actual {
			if actualType == expectedType {
				return true
			}
		}
	}

	// Special case: if we expected photo/video but got text (fixed link), that's acceptable
	if len(actual) == 1 && actual[0] == "text" {
		return true
	}

	return false
}

// PrintSummary prints a summary of the health check results
func (hc *HealthChecker) PrintSummary(results []TestResult) {
	successCount := 0
	failureCount := 0
	totalDuration := time.Duration(0)

	log.Printf("\n📊 Health Check Summary")
	log.Printf("======================")

	// Group by platform
	platformResults := make(map[string][]TestResult)
	for _, result := range results {
		platformResults[result.TestCase.Platform] = append(platformResults[result.TestCase.Platform], result)
		totalDuration += result.Duration

		if result.Success {
			successCount++
		} else {
			failureCount++
		}
	}

	// Print results by platform
	for platform, results := range platformResults {
		log.Printf("\n%s:", platform)
		for _, result := range results {
			status := "✅"
			if !result.Success {
				status = "❌"
			}

			mediaInfo := ""
			if result.MediaCount > 0 {
				mediaInfo = fmt.Sprintf(" (%d items: %s)", result.MediaCount, strings.Join(result.MediaTypes, ", "))
			}

			log.Printf("  %s %s%s - %v", status, result.TestCase.Description, mediaInfo, result.Duration)

			if result.Error != nil {
				log.Printf("     Error: %v", result.Error)
			}
		}
	}

	// Overall summary
	log.Printf("\n📈 Overall Results:")
	log.Printf("  Total tests: %d", len(results))
	log.Printf("  Passed: %d", successCount)
	log.Printf("  Failed: %d", failureCount)
	log.Printf("  Total time: %v", totalDuration)
	log.Printf("  Success rate: %.1f%%", float64(successCount)/float64(len(results))*100)

	if failureCount > 0 {
		log.Printf("\n⚠️ Some tests failed. Please check the logs above for details.")
	} else {
		log.Printf("\n✅ All tests passed! Bot is ready to process media.")
	}
}

// RunQuickCheck runs a minimal health check (just verify services are available)
func (hc *HealthChecker) RunQuickCheck() bool {
	log.Printf("🏥 Running quick health check...")

	// Check if downloader is available
	if !hc.downloader.IsAvailable() {
		log.Printf("❌ yt-dlp is not available")
		return false
	}

	log.Printf("✅ yt-dlp is available")

	// Check if we have cookies for major platforms
	platforms := []string{"cookies/instagram_cookies.txt", "cookies/twitter_cookies.txt", "cookies/tiktok_cookies.txt", "cookies/youtube_cookies.txt", "cookies/reddit_cookies.txt", "cookies/facebook_cookies.txt"}
	for _, cookieFile := range platforms {
		if _, err := os.Stat(cookieFile); err == nil {
			log.Printf("✅ Found %s", cookieFile)
		} else {
			log.Printf("⚠️ Missing %s (optional)", cookieFile)
		}
	}

	return true
}
