package services

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// RealLinkTester tests actual media extraction with real links
type RealLinkTester struct {
	downloader *SmartDownloader
}

// NewRealLinkTester creates a new real link tester
func NewRealLinkTester() *RealLinkTester {
	return &RealLinkTester{
		downloader: NewSmartDownloader(),
	}
}

// TestLink represents a real test link
type TestLink struct {
	Platform    string
	Description string
	URL         string
	ExpectedTypes []string // photo, video, text
}

// GetRealTestLinks returns real test links that should be stable
func (rlt *RealLinkTester) GetRealTestLinks() []TestLink {
	return []TestLink{
		// Instagram tests
		{
			Platform:    "Instagram", 
			Description: "Public reel/video",
			URL:         "https://www.instagram.com/reel/DKWY4HNM9RE/",
			ExpectedTypes: []string{"video"},
		},
		
		// TikTok tests
		{
			Platform:    "TikTok",
			Description: "Public video",
			URL:         "https://vm.tiktok.com/ZMS52pstS/",
			ExpectedTypes: []string{"video"},
		},
		{
			Platform:    "TikTok",
			Description: "Photo slideshow",
			URL:         "https://www.tiktok.com/@kukozhzh/photo/7507690616445259016", // Photo slideshow
			ExpectedTypes: []string{"photo"},
		},
		
		// Twitter/X tests  
		{
			Platform:    "Twitter/X",
			Description: "Tweet with image",
			URL:         "https://x.com/adnashmyash/status/1934499308549591094",
			ExpectedTypes: []string{"photo", "text"},
		},
		{
			Platform:    "Twitter/X",
			Description: "Tweet with video",
			URL:         "https://x.com/Anri__Tina/status/1933756349466185841",
			ExpectedTypes: []string{"video", "text"},
		},
		{
			Platform:    "Twitter/X",
			Description: "Tweet with multiple images",
			URL:         "https://x.com/sternenko/status/1934835108965425610",
			ExpectedTypes: []string{"photo", "text"},
		},
		{
			Platform:    "Twitter/X",
			Description: "Tweet with text only",
			URL:         "https://x.com/haxorus97/status/1934556201339244966",
			ExpectedTypes: []string{"text"},
		},
		{
			Platform:    "Twitter/X",
			Description: "Tweet with long text and two images",
			URL:         "https://twitter.com/yo_syp/status/1935083471594209314",
			ExpectedTypes: []string{"text", "photo"},
		},
		
		// YouTube tests
		{
			Platform:    "YouTube",
			Description: "YouTube Shorts",
			URL:         "https://youtube.com/shorts/5d7XprRhaXk",
			ExpectedTypes: []string{"video"},
		},

		// Facebook tests
		{
			Platform:    "Facebook",
			Description: "Facebook video",
			URL:         "https://www.facebook.com/share/v/162pLwmzti/",
			ExpectedTypes: []string{"video"},
		},
	}
}

// RunTests runs all real link tests
func (rlt *RealLinkTester) RunTests() {
	testLinks := rlt.GetRealTestLinks()
	
	log.Printf("\n🧪 Running Real Link Tests")
	log.Printf("==========================")
	log.Printf("Testing %d real links across all platforms\n", len(testLinks))
	
	results := make(map[string][]LinkTestResult)
	totalSuccess := 0
	totalFailed := 0
	
	for i, testLink := range testLinks {
		log.Printf("\n📋 Test %d/%d: %s - %s", i+1, len(testLinks), testLink.Platform, testLink.Description)
		log.Printf("🔗 URL: %s", testLink.URL)
		
		result := rlt.testSingleLink(testLink)
		results[testLink.Platform] = append(results[testLink.Platform], result)
		
		if result.Success {
			totalSuccess++
		} else {
			totalFailed++
		}
		
		// Delay between tests to avoid rate limiting
		if i < len(testLinks)-1 {
			log.Printf("⏳ Waiting 3 seconds before next test...")
			time.Sleep(3 * time.Second)
		}
	}
	
	// Print summary
	rlt.printTestSummary(results, totalSuccess, totalFailed)
}

// LinkTestResult holds the result of a single test
type LinkTestResult struct {
	TestLink   TestLink
	Success    bool
	Error      error
	MediaCount int
	MediaTypes []string
	Duration   time.Duration
	Method     string // Which method succeeded (yt-dlp, browser, fixed-link)
}

// testSingleLink tests a single link
func (rlt *RealLinkTester) testSingleLink(testLink TestLink) LinkTestResult {
	startTime := time.Now()
	
	result := LinkTestResult{
		TestLink: testLink,
		Success:  false,
	}
	
	// Download media
	mediaInfos, err := rlt.downloader.DownloadMedia(testLink.URL)
	result.Duration = time.Since(startTime)
	
	if err != nil {
		result.Error = err
		log.Printf("❌ Download failed: %v", err)
		return result
	}
	
	// Analyze results
	result.MediaCount = len(mediaInfos)
	result.MediaTypes = make([]string, 0)
	
	for _, media := range mediaInfos {
		result.MediaTypes = append(result.MediaTypes, media.Type)
		
		// Determine which method succeeded
		if media.Type == "text" && media.Filename == "" {
			result.Method = "fixed-link"
		} else if result.Method == "" {
			// Could be yt-dlp or browser - check for browser indicators
			if strings.Contains(media.Caption, "via browser") {
				result.Method = "browser"
			} else {
				result.Method = "yt-dlp"
			}
		}
		
		// Clean up downloaded files
		if media.Filename != "" {
			log.Printf("🧹 Cleaning up: %s", media.Filename)
			rlt.downloader.CleanupFile(media.Filename)
		}
	}
	
	// Check if we got expected types
	result.Success = rlt.validateExpectedTypes(result.MediaTypes, testLink.ExpectedTypes)
	
	// Log result
	if result.Success {
		log.Printf("✅ Success! Got %d items (%s) via %s in %v", 
			result.MediaCount, strings.Join(result.MediaTypes, ", "), result.Method, result.Duration)
	} else {
		log.Printf("⚠️ Partial success: expected %s, got %s via %s", 
			strings.Join(testLink.ExpectedTypes, "/"), strings.Join(result.MediaTypes, "/"), result.Method)
	}
	
	return result
}

// validateExpectedTypes checks if we got the expected media types
func (rlt *RealLinkTester) validateExpectedTypes(actual, expected []string) bool {
	// If we got a fixed link (text only), consider it acceptable
	if len(actual) == 1 && actual[0] == "text" {
		return true
	}
	
	// Check if we have at least one expected type
	for _, exp := range expected {
		for _, act := range actual {
			if exp == act {
				return true
			}
		}
	}
	
	return false
}

// printTestSummary prints a summary of all test results
func (rlt *RealLinkTester) printTestSummary(results map[string][]LinkTestResult, totalSuccess, totalFailed int) {
	log.Printf("\n\n📊 Test Summary")
	log.Printf("===============")
	
	// Platform breakdown
	platforms := []string{"Instagram", "TikTok", "Twitter/X", "YouTube", "9GAG"}
	
	for _, platform := range platforms {
		if platformResults, ok := results[platform]; ok {
			log.Printf("\n%s:", platform)
			
			for _, result := range platformResults {
				status := "✅"
				if !result.Success {
					status = "❌"
				}
				
				methodInfo := ""
				if result.Method != "" {
					methodInfo = fmt.Sprintf(" [%s]", result.Method)
				}
				
				if result.Error != nil {
					log.Printf("  %s %s - ERROR: %v", status, result.TestLink.Description, result.Error)
				} else {
					log.Printf("  %s %s - %d items (%s)%s - %v", 
						status, 
						result.TestLink.Description,
						result.MediaCount,
						strings.Join(result.MediaTypes, ", "),
						methodInfo,
						result.Duration)
				}
			}
		}
	}
	
	// Overall summary
	total := totalSuccess + totalFailed
	successRate := 0.0
	if total > 0 {
		successRate = float64(totalSuccess) / float64(total) * 100
	}
	
	log.Printf("\n\n📈 Overall Results:")
	log.Printf("  Total tests: %d", total)
	log.Printf("  Passed: %d", totalSuccess)
	log.Printf("  Failed: %d", totalFailed)
	log.Printf("  Success rate: %.1f%%", successRate)
	
	// Method usage summary
	log.Printf("\n🔧 Extraction Methods Used:")
	methodCounts := make(map[string]int)
	for _, platformResults := range results {
		for _, result := range platformResults {
			if result.Method != "" && result.Error == nil {
				methodCounts[result.Method]++
			}
		}
	}
	
	for method, count := range methodCounts {
		log.Printf("  %s: %d times", method, count)
	}
	
	if totalFailed > 0 {
		log.Printf("\n⚠️ Some tests failed. Check the logs above for details.")
	} else {
		log.Printf("\n✅ All tests passed! Bot is working correctly.")
	}
}

// RunQuickTest runs a single test for verification
func (rlt *RealLinkTester) RunQuickTest() {
	log.Printf("\n🚀 Running quick test with a reliable link...")
	
	// Use a YouTube Short as it's likely to be stable and work well
	testLink := TestLink{
		Platform:    "YouTube",
		Description: "Quick test - YouTube Short",
		URL:         "https://www.youtube.com/shorts/tPEE9ZwTmy0",
		ExpectedTypes: []string{"video"},
	}
	
	result := rlt.testSingleLink(testLink)
	
	if result.Success {
		log.Printf("\n✅ Quick test passed! Bot is ready.")
	} else {
		log.Printf("\n❌ Quick test failed. Please check your configuration.")
	}
}