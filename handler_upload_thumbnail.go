package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// Parsing form data for multipart files
	const maxMemory = 10 << 20
	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form data", err)
		return
	}

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to get form file", err)
		return
	}
	defer file.Close()

	// Get and validate the media type
	contentType := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid content type", err)
		return
	}

	// Validate that only JPEG and PNG images are allowed
	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Only JPEG and PNG images are allowed", nil)
		return
	}

	// Determine file extension from media type
	fileExtension := getFileExtension(mediaType)
	
	// Create unique filename (Generate 32 random bytes)
	randomBytes := make([]byte, 32)
	_, err = rand.Read(randomBytes)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to generate random bytes", err)
		return
	}

	// Convert to base64 URL-safe string
	randomString := base64.RawURLEncoding.EncodeToString(randomBytes)

	// Create filename
	filename := randomString + fileExtension
	filePath := filepath.Join(cfg.assetsRoot, filename)

	// Create the file on disk
	outFile, err := os.Create(filePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create file", err)
		return
	}
	defer outFile.Close()

	// Copy the file content to disk
	_, err = io.Copy(outFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to save file", err)
		return
	}

	// Get the video's metadata from the database
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}

	// Check if the authenticated user is the video owner
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User is not the video owner", nil)
		return
	}

	// Create the thumbnail URL pointing to the assets directory
	thumbnailURL := fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, filename)

	// Update the video with the file URL
	updatedVideo := video // Copy existing video
	updatedVideo.UpdatedAt = time.Now()
	updatedVideo.ThumbnailURL = &thumbnailURL

	// Update video in database
	err = cfg.db.UpdateVideo(updatedVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, updatedVideo)
}

// Helper function to map media types to file extensions
func getFileExtension(mediaType string) string {
	switch mediaType {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	default:
		return ".jpg" // default fallback (shouldn't happen due to validation above)
	}
}