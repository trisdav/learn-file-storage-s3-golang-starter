package main

import (
	"fmt"
	"net/http"
	"io"
	"os"
	"strings"
	"errors"
	"encoding/base64"
	"crypto/rand"

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

	// TODO: implement the upload here
	const maxMemory = 10<<20

	parsErr := r.ParseMultipartForm(maxMemory)
	if parsErr != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse multipart form", parsErr)
		return
	}

	file, header, formErr := r.FormFile("thumbnail")
	if formErr != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse thumbnail", formErr)
		return
	}
	defer file.Close()
	media := header.Header.Get("Content-Type")

	tData, readErr := io.ReadAll(file)
	if readErr != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse thumbnail data", readErr)
		return
	}

	vid, getErr := cfg.db.GetVideo(videoID)
	if getErr != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't retrive video data from the database", getErr)
		return
	}

	if vid.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You are not the author of that video", nil)
		return	
	}

	// Store image
		//Build url
	ext, imgErr := getImgExt(media) 
	if imgErr != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid image format", imgErr)
		return
	}
	
			//http://localhost:<port>/assets/<videoID>.<file_extension>
	imgNameBytes := make([]byte,16)

	_, randReadErr := rand.Read(imgNameBytes)
	if randReadErr != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to create random image name", randReadErr)
		return
	}

	imgName := base64.RawURLEncoding.EncodeToString(imgNameBytes)

	imagePath := fmt.Sprintf("http://localhost:8091/assets/%s.%s",imgName,ext)
	vid.ThumbnailURL = &imagePath
		// write image image
	writErr := os.WriteFile(fmt.Sprintf("assets/%s.%s",imgName,ext), tData,0644)
	if writErr != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't retrive video data from the database", writErr)
		return
	}
	// Update video data
	updatErr := cfg.db.UpdateVideo(vid)
	if updatErr != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't update video data in the database", updatErr)
		return
	}
	respondWithJSON(w, http.StatusOK, vid)
}

func getImgExt(contentType string) (string, error) {
	parts:=strings.Split(contentType, "/")
	if len(parts) < 2 {
		return "", errors.New("Failed to parse extension")
	}
	if parts[1] == "png" || parts[1] == "jpeg" || parts[1]=="jpg" {
		return parts[1], nil
	} else {
		return "", errors.New("Invalid file type for image")
	}	
}