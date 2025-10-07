package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
)

// Struct to parse ffprobe JSON output
type FFProbeOutput struct {
	Streams []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"streams"`
}

func getVideoAspectRatio(filePath string) (string, error) {
	// Run ffprobe command
	cmd := exec.Command("ffprobe", 
		"-v", "error",
		"-print_format", "json", 
		"-show_streams", 
		filePath,
	)
	
	// Capture stdout
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	
	// Run the command
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("ffprobe failed: %w", err)
	}
	
	// Parse JSON output
	var probeOutput FFProbeOutput
	err = json.Unmarshal(stdout.Bytes(), &probeOutput)
	if err != nil {
		return "", fmt.Errorf("failed to parse ffprobe output: %w", err)
	}
	
	// Check if we have video streams
	if len(probeOutput.Streams) == 0 {
		return "other", nil
	}
	
	// Get first stream's dimensions
	width := probeOutput.Streams[0].Width
	height := probeOutput.Streams[0].Height
	
	if width == 0 || height == 0 {
		return "other", nil
	}
	
	// Calculate aspect ratio and determine category
	return categorizeAspectRatio(width, height), nil
}

func categorizeAspectRatio(width, height int) string {
	// Calculate aspect ratio as float for comparison
	ratio := float64(width) / float64(height)
	
	// Define aspect ratio targets with tolerance
	landscapeTarget := 16.0 / 9.0   // ≈1.777
	portraitTarget := 9.0 / 16.0    // ≈0.5625
	tolerance := 0.1                // 10% tolerance
	
	// Check if close to landscape (16:9)
	if ratio >= landscapeTarget-tolerance && ratio <= landscapeTarget+tolerance {
		return "landscape"
	}
	
	// Check if close to portrait (9:16)  
	if ratio >= portraitTarget-tolerance && ratio <= portraitTarget+tolerance {
		return "portrait"
	}
	
	// Everything else
	return "other"
}

/*
Simple Explanation: What's Happening with MP4 Videos

🎬 MP4 file like a movie on a DVD:
Normal MP4 (without "fast start"):

. The table of contents (called "moov atom") is at the END of the DVD

. To play the movie, you need the table of contents first

. So the browser has to:

	1. Request the whole movie to find the table of contents

	2. Jump to the end to get the table of contents

	3. Jump back to the beginning to actually play the movie

. This causes 3 separate requests and makes videos start slower

Fast Start MP4:

. The table of contents is at the BEGINNING of the DVD

. Browser can start playing immediately

. Only 1 request needed

. Videos start instantly

*/

// Function that moves the moov atom (Table of content) to the beginning of the MP4 file.
func processVideoForFastStart(inputPath string) (string, error) {
	// Create output file path (add .processing to original)
	outputPath := inputPath + ".processing"

	// Run ffmpeg to create fast-start version
	cmd := exec.Command("ffmpeg",
		"-i", inputPath,    // Input file
		"-c", "copy",       // Copy without re-encoding (fast)
		"-movflags", "faststart", // Move moov atom to beginning
		"-f", "mp4",        // Output format
		outputPath,         // Output file
	)

	// Run the command
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("ffmpeg faststart failed: %w", err)
	}

	return outputPath, nil
}
