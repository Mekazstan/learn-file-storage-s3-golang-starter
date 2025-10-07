package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	// Step 1: Extract videoID from request path
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	// Step 2: Authenticate user to get userID
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	// Step 3: Get video metadata
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}

	// Step 4: Check ownership of video
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User is not the video owner", nil)
		return
	}

	fmt.Println("uploading video", videoID, "by user", userID)

	// Step 5: Set upload limit & parse form
	const maxUploadSize = 1 << 30 // 1GB
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	err = r.ParseMultipartForm(maxUploadSize)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form data", err)
		return
	}

	// Get the video file from form
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to get video file", err)
		return
	}
	defer file.Close()

	// Step 6: Validate it's an MP4
	contentType := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid content type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Only MP4 videos are allowed", nil)
		return
	}

	// Step 7: Save to temp file (Enable streaming files to disk & then to S3 & avoiding memory overload. Also for network resilience)
	tempFile, err := os.CreateTemp("", "tubely-upload-*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create temp file", err)
		return
	}
	defer os.Remove(tempFile.Name()) // Clean up temp file
	defer tempFile.Close()

	// Copy uploaded file to temp file
	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to save video to temp file", err)
		return
	}

	// Close the temp file so ffmpeg can access it
	tempFile.Close()

	// Step 7b: Process video for fast start in-order to enable video streaming before uploading to S3
	fmt.Println("Processing video for fast start...")
	processedPath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to process video for fast start", err)
		return
	}
	defer os.Remove(processedPath) // Clean up processed file

	// Open the processed file for S3 upload
	processedFile, err := os.Open(processedPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to open processed video", err)
		return
	}
	defer processedFile.Close()

	// Generate random filename
	randomBytes := make([]byte, 32)
	_, err = rand.Read(randomBytes)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to generate random filename", err)
		return
	}

	randomString := base64.RawURLEncoding.EncodeToString(randomBytes)

	// Detect video aspect ratio
	aspectRatio, err := getVideoAspectRatio(processedPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to analyze video", err)
		return
	}

	fmt.Printf("Detected video aspect ratio: %s\n", aspectRatio)

	// Create S3 key with aspect ratio prefix
	fileKey := fmt.Sprintf("%s/%s.mp4", aspectRatio, randomString)

	// Step 8: Upload to S3 with retry logic
	maxRetries := 3
	var uploadErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Reset file pointer to beginning for each retry
		_, seekErr := processedFile.Seek(0, io.SeekStart)
		if seekErr != nil {
			uploadErr = seekErr
			break
		}

		_, uploadErr = cfg.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
			Bucket:      aws.String(cfg.s3Bucket),
			Key:         aws.String(fileKey),
			Body:        processedFile,
			ContentType: aws.String("video/mp4"),
		})

		if uploadErr == nil {
			// Success!
			break
		}

		fmt.Printf("S3 upload attempt %d failed: %v\n", attempt, uploadErr)

		// If not the last attempt, wait before retrying
		if attempt < maxRetries {
			backoffTime := time.Second * time.Duration(attempt) // 1s, 2s, 3s
			time.Sleep(backoffTime)
		}
	}

	if uploadErr != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to upload to S3 after retries", uploadErr)
		return
	}

	// Step 9: Update DB with S3 URL
	videoURL := fmt.Sprintf("%s,%s", cfg.s3Bucket, fileKey)

	// Update the video with the S3 URL
	updatedVideo := video // Copy existing video
	updatedVideo.UpdatedAt = time.Now()
	updatedVideo.VideoURL = &videoURL

	// Update video in database
	err = cfg.db.UpdateVideo(updatedVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update video", err)
		return
	}

	// Convert to signed video for response
	signedVideo, err := cfg.dbVideoToSignedVideo(updatedVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to generate signed URL", err)
		return
	}

	respondWithJSON(w, http.StatusOK, signedVideo)
}
