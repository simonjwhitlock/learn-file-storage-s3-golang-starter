package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 1 << 30

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

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to retreve video with matching ID", err)
		return
	}
	if userID != video.UserID {
		respondWithError(w, http.StatusUnauthorized, "video not owneed by user", nil)
		return
	}
	r.ParseMultipartForm(maxMemory)

	videoData, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't parse form file", err)
		return
	}
	defer videoData.Close()

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

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Only JPEG and PNG thumbnails are allowed", nil)
		return
	}

	tempFile, err := os.CreateTemp("", "tubley_upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to create temp video file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, videoData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to write umpladed data to temp file", err)
		return
	}

	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed seek to start of temp file", err)
		return
	}

	aspect, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to get video aspect ratio:", err)
		return
	}
	fmt.Println(aspect)

	processedFilePath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to process for fast start:", err)
		return
	}
	defer os.Remove(processedFilePath)
	openedProcessedFile, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error opening file", err)
		return
	}

	randSlice := make([]byte, 32)
	rand.Read(randSlice)
	videoFileID := base64.RawURLEncoding.EncodeToString(randSlice)
	videoFileName := fmt.Sprintf("%s/%s.mp4", aspect, videoFileID)
	fmt.Println(videoFileID)
	fmt.Println(videoFileName)

	putObjectInput := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &videoFileName,
		Body:        openedProcessedFile,
		ContentType: &mediaType,
	}

	_, err = cfg.s3Client.PutObject(context.Background(), &putObjectInput)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't upload file to S3", err)
	}

	videoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, videoFileName)
	fmt.Println(cfg.s3Bucket)
	fmt.Println(cfg.s3Region)
	fmt.Println(videoFileName)
	fmt.Println(videoURL)
	video.VideoURL = &videoURL
	err = cfg.db.UpdateVideo(video)
	// metadata.VideoURL = &fileURL
	// err = cfg.db.UpdateVideo(metadata)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", nil)
		return
	}
}

func getVideoAspectRatio(filePath string) (string, error) {
	streams := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var buffer bytes.Buffer
	streams.Stdout = &buffer
	if err := streams.Run(); err != nil {
		return "", fmt.Errorf("ffprobe error: %v", err)
	}

	var output struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}

	json.Unmarshal(buffer.Bytes(), &output)

	if len(output.Streams) == 0 {
		return "", fmt.Errorf("ffprobe failed to extract streams")
	}

	aspectRaio := float32(output.Streams[0].Width) / float32(output.Streams[0].Height)
	fmt.Println(aspectRaio)
	if aspectRaio <= 1.778 && aspectRaio >= 1.777 {
		return "landscape", nil
	} else if aspectRaio <= 0.563 && aspectRaio >= 0.562 {
		return "portrait", nil
	} else {
		return "other", nil
	}

}

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := fmt.Sprintf("%s.processing", filePath)

	cmd := exec.Command("ffmpeg",
		"-i", filePath,
		"-c", "copy",
		"-movflags", "faststart",
		"-f", "mp4",
		outputFilePath)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("could not parse ffprobe output: %s, %v", stderr.String(), err)
	}

	fileInfo, err := os.Stat(outputFilePath)
	if err != nil {
		return "", fmt.Errorf("could not stat processed file: %v", err)
	}
	if fileInfo.Size() == 0 {
		return "", fmt.Errorf("processed file is empty")
	}

	return outputFilePath, nil
}
