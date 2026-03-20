package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 10 << 20

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
	r.ParseMultipartForm(maxMemory)

	thumbnailFile, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't parse form file", err)
		return
	}
	defer thumbnailFile.Close()

	rawMediaType := header.Header.Get("Content-Type")
	if rawMediaType == "" {
		respondWithError(w, http.StatusBadRequest, "Missing Content Type header for thumbnail", nil)
		return
	}

	mediaType, _, err := mime.ParseMediaType(rawMediaType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type format", err)
		return
	}

	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Only JPEG and PNG thumbnails are allowed", nil)
		return
	}

	data, err := io.ReadAll(thumbnailFile)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "internal error reading thumbnail data", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to retreve video with matching ID", err)
		return
	}
	if userID != video.UserID {
		respondWithError(w, http.StatusUnauthorized, "video not owneed by user", nil)
		return
	}

	thumbnailData := thumbnail{
		data:      data,
		mediaType: mediaType,
	}
	videoThumbnails[videoID] = thumbnailData
	thumbnailURL := fmt.Sprintf("http://localhost:%s/api/thumbnails/%s", cfg.port, videoIDString)
	video.ThumbnailURL = &thumbnailURL
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to update video thumbnail", err)
		return
	}
	fmt.Println("thumbnailURL: %s", thumbnailURL)
	respondWithJSON(w, http.StatusOK, video)
}
