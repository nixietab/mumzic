package youtubedl

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/jpeg"
	_ "image/png"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/nfnt/resize"
	"layeh.com/gumble/gumbleffmpeg"
)

// IsWhiteListedURL returns true if URL begins with an acceptable URL for ytdl
// ! Don't forget to end url with / !
func IsWhiteListedURL(url string) bool {
	whiteListedURLS := []string{
		"https://www.youtube.com/", 
		"https://music.youtube.com/", 
		"https://youtu.be/", 
		"https://soundcloud.com/",
	}
	for i := range whiteListedURLS {
		if strings.HasPrefix(url, whiteListedURLS[i]) {
			return true
		}
	}
	return false
}

// SearchYouTube searches for a video on YouTube and returns URL and error
func SearchYouTube(query string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	ytDL := exec.CommandContext(ctx, "yt-dlp", "--no-playlist", "--get-id", "--default-search", "ytsearch1", query)
	var output bytes.Buffer
	ytDL.Stdout = &output
	err := ytDL.Run()
	if err != nil {
		log.Println("Youtube-DL failed to search for:", query)
		return "", err
	}

	videoID := strings.TrimSpace(output.String())
	if videoID == "" {
		return "", nil
	}
	return "https://www.youtube.com/watch?v=" + videoID, nil
}

// SearchYouTubeSimple maintains backward compatibility (single return value)
func SearchYouTubeSimple(query string) string {
	url, _ := SearchYouTube(query)
	return url
}

// SearchYouTubeAsync provides async version
func SearchYouTubeAsync(query string) <-chan SearchResult {
	resultChan := make(chan SearchResult, 1)
	go func() {
		url, err := SearchYouTube(query)
		resultChan <- SearchResult{URL: url, Err: err}
	}()
	return resultChan
}

type SearchResult struct {
	URL string
	Err error
}

func GetYtDLTitle(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	ytDL := exec.CommandContext(ctx, "yt-dlp", "--no-playlist", "-e", url)
	var output bytes.Buffer
	ytDL.Stdout = &output
	err := ytDL.Run()
	if err != nil {
		log.Println("Youtube-DL failed to get title for", url)
		return "", err
	}
	return output.String(), nil
}

// GetYtDLTitleSimple maintains backward compatibility
func GetYtDLTitleSimple(url string) string {
	title, _ := GetYtDLTitle(url)
	return title
}

// GetYtDLTitleAsync provides async version
func GetYtDLTitleAsync(url string) <-chan TitleResult {
	resultChan := make(chan TitleResult, 1)
	go func() {
		title, err := GetYtDLTitle(url)
		resultChan <- TitleResult{Title: title, Err: err}
	}()
	return resultChan
}

type TitleResult struct {
	Title string
	Err   error
}

func GetYtDLSource(url string) gumbleffmpeg.Source {
	return gumbleffmpeg.SourceExec("yt-dlp", "--no-playlist", "-f", "bestaudio", "--rm-cache-dir", "-q", "-o", "-", url)
}
// GetYtDLThumbnail fetches the thumbnail for a YouTube video and returns it as base64-encoded data
func GetYtDLThumbnail(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
	defer cancel()

	ytDL := exec.CommandContext(ctx, "yt-dlp", "--no-playlist", "--get-thumbnail", url)
	var output bytes.Buffer
	ytDL.Stdout = &output
	err := ytDL.Run()
	if err != nil {
		log.Println("Youtube-DL failed to get thumbnail URL for", url, ":", err)
		return "", err
	}

	thumbnailURL := strings.TrimSpace(output.String())
	if thumbnailURL == "" {
		return "", nil
	}

		// Try to get a different thumbnail format by modifying the URL
		// YouTube thumbnails often have multiple formats available
		// #nosec G107 -- thumbnailURL is from yt-dlp for a whitelisted video, considered safe
	if strings.Contains(thumbnailURL, ".webp") {
		jpegURL := strings.Replace(thumbnailURL, ".webp", ".jpg", 1)
		jpegURL = strings.Replace(jpegURL, "_webp", "", 1)
		resp, err := http.Head(jpegURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			thumbnailURL = jpegURL
		}
	}

	// Download the thumbnail		
	// #nosec G107
	resp, err := http.Get(thumbnailURL)
	if err != nil {
		log.Println("Failed to download thumbnail from", thumbnailURL, ":", err)
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		log.Println("Failed to decode thumbnail image:", err)
		return "", err
	}

	resizedImg := resize.Resize(100, 100, img, resize.Lanczos3)

	jpegQuality := 60
	maxSize := 4000
	var buf bytes.Buffer
	var encodedStr string

	for maxSize >= 4000 && jpegQuality > 0 {
		buf.Reset()
		options := jpeg.Options{Quality: jpegQuality}
		if err := jpeg.Encode(&buf, resizedImg, &options); err != nil {
			log.Println("Error encoding jpg for base64:", err)
			return "", err
		}
		encodedStr = "<img src=\"data:image/jpeg;base64, " + base64.StdEncoding.EncodeToString(buf.Bytes()) + "\" />"
		maxSize = len(encodedStr)
		jpegQuality -= 10
	}
	// Check if the image is too large
	if len(encodedStr) > 4850 {
		return "", nil
	}

	return encodedStr, nil
}

// GetYtDLThumbnailAsync provides async version
func GetYtDLThumbnailAsync(url string) <-chan ThumbnailResult {
	resultChan := make(chan ThumbnailResult, 1)
	go func() {
		thumbnail, err := GetYtDLThumbnail(url)
		resultChan <- ThumbnailResult{Thumbnail: thumbnail, Err: err}
	}()
	return resultChan
}

type ThumbnailResult struct {
	Thumbnail string
	Err       error
}

// BatchGetInfo gets title and thumbnail concurrently
func BatchGetInfo(url string) (title string, thumbnail string, err error) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		title, err = GetYtDLTitle(url)
	}()

	go func() {
		defer wg.Done()
		thumbnail, err = GetYtDLThumbnail(url)
	}()

	wg.Wait()
	return title, thumbnail, err
}

// BatchGetInfoAsync provides async version
func BatchGetInfoAsync(url string) <-chan BatchInfoResult {
	resultChan := make(chan BatchInfoResult, 1)
	go func() {
		title, thumbnail, err := BatchGetInfo(url)
		resultChan <- BatchInfoResult{
			Title:     title,
			Thumbnail: thumbnail,
			Err:       err,
		}
	}()
	return resultChan
}

type BatchInfoResult struct {
	Title     string
	Thumbnail string
	Err       error
}