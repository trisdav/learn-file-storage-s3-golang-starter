package main

import (
	"fmt"
	"net/http"
	"io"

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

	newTn := thumbnail{data:tData,mediaType:media,}
	videoThumbnails[videoID] = newTn
	newTnUrl := fmt.Sprintf("http://localhost:8091/api/thumbnails/%s",videoID.String())
	vid.ThumbnailURL = &newTnUrl
	updatErr := cfg.db.UpdateVideo(vid)
	if updatErr != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't update video data in the database", updatErr)
		return
	}

	respondWithJSON(w, http.StatusOK, vid)
}
