package main

import (
	"net/http"
	"github.com/google/uuid"
	"os"
	"io"
	"fmt"
	"encoding/base64"
	"crypto/rand"
	"strings"
	"errors"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	// Authenticate user
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

	// Get video and verify user owns it
	vid, getErr := cfg.db.GetVideo(videoID)
	if getErr != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't retrive video data from the database", getErr)
		return
	}

	if vid.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You are not the author of that video", nil)
		return	
	}

	// Read metadata and file data
	const maxMemory = 10<<20

	parsErr := r.ParseMultipartForm(maxMemory)
	if parsErr != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse multipart form", parsErr)
		return
	}

	file, header, formErr := r.FormFile("video")
	if formErr != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse video", formErr)
		return
	}
	defer file.Close()
	media := header.Header.Get("Content-Type")

	// store video
		// verify format
	_, mp4Err := getMp4Ext(media) 
	if mp4Err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video format", mp4Err)
		return
	}

		// create temp file
	tmpFile, tempErr := os.CreateTemp("","video-*.mp4")
	if tempErr != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to create tmp file", tempErr)
		return
	}
		// write to temp file
	if _, filErr := io.Copy(tmpFile,file); filErr != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to write to tmp file", filErr)
		return
	}
    defer os.Remove(tmpFile.Name())
    defer tmpFile.Close()

	tmpFile.Seek(0,io.SeekStart)

	// upload to cloud
		// Make a random name
	vidNameBytes := make([]byte,16)

	_, randReadErr := rand.Read(vidNameBytes)
	if randReadErr != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to create random video name", randReadErr)
		return
	}
	vidName := fmt.Sprintf("%s.%s",base64.RawURLEncoding.EncodeToString(vidNameBytes), ".mp4")


	cloudInput := &s3.PutObjectInput {
		Bucket: aws.String("tubely-65365"),
		Key: aws.String(vidName),
		Body: tmpFile,
		ContentType: aws.String("video/mp4"),

	}
	_, cloudErr := cfg.s3Client.PutObject(r.Context(),cloudInput)	
	if cloudErr != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to write to cloud", cloudErr)
		return
	}

	videoPath := fmt.Sprintf("http://tubely-65365.s3.us-east-2.amazonaws.com/%s",vidName)
	vid.VideoURL = &videoPath

	// Update video data
	updatErr := cfg.db.UpdateVideo(vid)
	if updatErr != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't update video data in the database", updatErr)
		return
	}
	respondWithJSON(w, http.StatusOK, vid)
}

func getMp4Ext(contentType string) (string, error) {
	parts:=strings.Split(contentType, "/")
	if len(parts) < 2 {
		return "", errors.New("Failed to parse extension")
	}
	if parts[1] == "mp4" {
		return parts[1], nil
	} else {
		return "", errors.New("Invalid file type for image")
	}	
}
