package services

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StartupChecker performs basic checks when the bot starts
type StartupChecker struct {
	workingDir string
}

// NewStartupChecker creates a new startup checker
func NewStartupChecker() *StartupChecker {
	workingDir, _ := os.Getwd()
	return &StartupChecker{
		workingDir: workingDir,
	}
}

// RunStartupChecks performs all startup checks
func (sc *StartupChecker) RunStartupChecks() {
	log.Printf("🚀 Running startup checks...")
	log.Printf("📂 Working directory: %s", sc.workingDir)
	
	// Check core dependencies
	sc.checkYtDlp()
	sc.checkFfmpeg()
	sc.checkChrome()
	
	// Check authentication files
	sc.checkAuthFiles()
	
	// Check directories
	sc.checkDirectories()
	
	// Print platform support summary
	sc.printPlatformSupport()
	
	log.Printf("✅ Startup checks completed")
}

// checkYtDlp checks if yt-dlp is available and working
func (sc *StartupChecker) checkYtDlp() {
	log.Printf("\n🎬 Checking yt-dlp...")
	
	// Check various possible locations
	candidates := []string{
		filepath.Join(sc.workingDir, "bin", "yt-dlp"),
		"/var/www/social-link-tg-bot/bin/yt-dlp",
		"yt-dlp",
		"youtube-dl",
	}
	
	found := false
	for _, path := range candidates {
		cmd := exec.Command(path, "--version")
		if output, err := cmd.Output(); err == nil {
			version := string(output)
			log.Printf("  ✅ Found yt-dlp at: %s", path)
			log.Printf("  📌 Version: %s", version[:len(version)-1]) // Remove newline
			found = true
			break
		}
	}
	
	if !found {
		log.Printf("  ❌ yt-dlp not found! Media downloads will not work.")
		log.Printf("  💡 Install with: pip install yt-dlp")
	}
}

// checkFfmpeg checks if ffmpeg is available for video conversion
func (sc *StartupChecker) checkFfmpeg() {
	log.Printf("\n🎥 Checking ffmpeg...")
	
	candidates := []string{
		"ffmpeg",
		"/usr/bin/ffmpeg",
		"/usr/local/bin/ffmpeg",
		"/home/botuser/bin/ffmpeg",
		filepath.Join(sc.workingDir, "bin/ffmpeg"),
	}
	
	found := false
	for _, path := range candidates {
		cmd := exec.Command(path, "-version")
		if output, err := cmd.Output(); err == nil {
			lines := strings.Split(string(output), "\n")
			if len(lines) > 0 {
				log.Printf("  ✅ Found ffmpeg at: %s", path)
				log.Printf("  📌 Version: %s", lines[0])
				found = true
				break
			}
		}
	}
	
	if !found {
		log.Printf("  ⚠️ ffmpeg not found. WebM to MP4 conversion will not work.")
		log.Printf("  💡 Install with: sudo apt install ffmpeg")
	}
}

// checkChrome checks if Chrome/Chromium is available for browser extraction
func (sc *StartupChecker) checkChrome() {
	log.Printf("\n🌐 Checking Chrome/Chromium...")
	
	homeDir := os.Getenv("HOME")
	candidates := []string{
		filepath.Join(homeDir, "chrome/chrome"),
		"/usr/bin/google-chrome",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
	}
	
	found := false
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			cmd := exec.Command(path, "--version")
			if output, err := cmd.Output(); err == nil {
				log.Printf("  ✅ Found Chrome at: %s", path)
				log.Printf("  📌 Version: %s", strings.TrimSpace(string(output)))
				found = true
				break
			}
		}
	}
	
	if !found {
		log.Printf("  ⚠️ Chrome/Chromium not found. Browser fallback extraction will not work.")
		log.Printf("  💡 Chrome can be installed for enhanced extraction capabilities")
	}
}

// checkAuthFiles checks for authentication cookie files
func (sc *StartupChecker) checkAuthFiles() {
	log.Printf("\n🍪 Checking authentication files...")
	
	authFiles := map[string]string{
		"Instagram":  "cookies/instagram_cookies.txt",
		"Twitter/X":  "cookies/twitter_cookies.txt",
		"TikTok":    "cookies/tiktok_cookies.txt",
		"YouTube":   "cookies/youtube_cookies.txt",
		"Reddit":    "cookies/reddit_cookies.txt",
		"Facebook":  "cookies/facebook_cookies.txt",
		"9GAG":      "cookies/9gag_cookies.txt",
	}
	
	for platform, filename := range authFiles {
		path := filepath.Join(sc.workingDir, filename)
		if stat, err := os.Stat(path); err == nil {
			log.Printf("  ✅ %s: Found (%d bytes)", platform, stat.Size())
		} else {
			log.Printf("  ⚠️ %s: Not found (reduced functionality)", platform)
		}
	}
}

// checkDirectories checks required directories
func (sc *StartupChecker) checkDirectories() {
	log.Printf("\n📁 Checking directories...")
	
	dirs := []string{
		"temp",
		"cache",
		"logs",
	}
	
	for _, dir := range dirs {
		path := filepath.Join(sc.workingDir, dir)
		if stat, err := os.Stat(path); err == nil && stat.IsDir() {
			log.Printf("  ✅ %s/: Exists", dir)
		} else {
			// Try to create it
			if err := os.MkdirAll(path, 0755); err == nil {
				log.Printf("  ✅ %s/: Created", dir)
			} else {
				log.Printf("  ❌ %s/: Failed to create", dir)
			}
		}
	}
}

// printPlatformSupport prints a summary of platform support
func (sc *StartupChecker) printPlatformSupport() {
	log.Printf("\n📱 Platform Support Summary:")
	log.Printf("================================")
	
	platforms := []struct {
		Name        string
		Supported   []string
		FixedLink   string
	}{
		{
			Name:      "Instagram",
			Supported: []string{"Photos", "Videos", "Reels", "Carousels", "Stories"},
			FixedLink: "instagramez.com",
		},
		{
			Name:      "TikTok",
			Supported: []string{"Videos", "Photo slideshows"},
			FixedLink: "vxtiktok.com",
		},
		{
			Name:      "Twitter/X",
			Supported: []string{"Photos", "Videos", "GIFs", "Text"},
			FixedLink: "fixupx.com",
		},
		{
			Name:      "YouTube",
			Supported: []string{"Shorts", "Regular videos (with size limit)"},
			FixedLink: "none",
		},
		{
			Name:      "9GAG",
			Supported: []string{"Photos", "Videos (WebM → MP4 conversion)"},
			FixedLink: "none",
		},
	}
	
	for _, p := range platforms {
		log.Printf("\n%s:", p.Name)
		log.Printf("  Supported: %s", strings.Join(p.Supported, ", "))
		if p.FixedLink != "none" {
			log.Printf("  Fallback: %s", p.FixedLink)
		}
	}
	
	log.Printf("\n================================")
}