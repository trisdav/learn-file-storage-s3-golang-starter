package main

import (
	"net/http"
	"github.com/google/uuid"
	"os"
	"os/exec"
	"encoding/json"
	"io"
	"fmt"
	"encoding/base64"
	"crypto/rand"
	"strings"
	"errors"
	"bytes"
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

	prefix,pErr:=getVideoAspectRatio(tmpFile.Name())
	if pErr != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Failed to prase aspect ratio: %v",pErr), pErr)
		return
	}

	vidName := fmt.Sprintf("%s/%s.%s",prefix,base64.RawURLEncoding.EncodeToString(vidNameBytes), "mp4")

	fastFilePath, processErr := processVideoForFastStart(tmpFile.Name()) 
	if processErr != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Failed to set fast start: %v",processErr), processErr)
		return
	}

	fastFile, fastErr := os.Open(fastFilePath)
	if fastErr != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Failed to open fast start mp4: %v",fastErr), fastErr)
		return
	}
	defer fastFile.Close()

	s3Bucket := cfg.s3Bucket
	cloudInput := &s3.PutObjectInput {
		Bucket: aws.String(s3Bucket),
		Key: aws.String(vidName),
		Body: fastFile,
		ContentType: aws.String("video/mp4"),
	}
	_, cloudErr := cfg.s3Client.PutObject(r.Context(),cloudInput)	
	if cloudErr != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to write to cloud", cloudErr)
		return
	}

	videoPath := fmt.Sprintf("http://%s.s3.us-east-2.amazonaws.com/%s",s3Bucket,vidName)
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


func getVideoAspectRatio(filePath string) (string,error) {
	cmd := strings.Split(fmt.Sprintf("ffprobe -v error -print_format json -show_streams %s", filePath)," ")
	output := new(bytes.Buffer)
	exeCmd := exec.Command(cmd[0],cmd[1:]...)
	exeCmd.Stdout = output
	execErr := exeCmd.Run()
	if execErr != nil {
		return "", errors.New("Failed to execute ffprobe")
	}
	var jsObj mp4MetaData
	unErr := json.Unmarshal(output.Bytes(),&jsObj)
	if unErr != nil {
		return "", errors.New("Failed to unmarshal ffprobe output")
	}
	ratio := float64(jsObj.Streams[0].Width) / float64(jsObj.Streams[0].Height)
	if ratio > 1.7  && ratio < 1.8 {
		return "landscape", nil //"16:9", nil
	} else if ratio > 0.5 && ratio < 0.6 {
		return "portrait", nil  //"9:16", nil
	} else {
		return "other", nil
	}
}

func processVideoForFastStart(filePath string) (string, error) {
	outputPath:=fmt.Sprintf("%s%s",filePath,".processing")
	cmd := strings.Split(fmt.Sprintf("ffmpeg -i %s -c copy -movflags faststart -f mp4 %s", filePath, outputPath)," ")
	output := new(bytes.Buffer)
	exeCmd := exec.Command(cmd[0],cmd[1:]...)
	exeCmd.Stdout = output
	exeCmd.Stderr = output
	execErr := exeCmd.Run()
	if execErr != nil {
		return "", errors.New(fmt.Sprintf("Failed to execute ffmpeg fstart: %v",output))
	}
	return outputPath, nil
}

type mp4MetaData struct {
	Streams []struct {
		Index              int    `json:"index"`
		CodecName          string `json:"codec_name,omitempty"`
		CodecLongName      string `json:"codec_long_name,omitempty"`
		Profile            string `json:"profile,omitempty"`
		CodecType          string `json:"codec_type"`
		CodecTagString     string `json:"codec_tag_string"`
		CodecTag           string `json:"codec_tag"`
		Width              int    `json:"width,omitempty"`
		Height             int    `json:"height,omitempty"`
		CodedWidth         int    `json:"coded_width,omitempty"`
		CodedHeight        int    `json:"coded_height,omitempty"`
		ClosedCaptions     int    `json:"closed_captions,omitempty"`
		FilmGrain          int    `json:"film_grain,omitempty"`
		HasBFrames         int    `json:"has_b_frames,omitempty"`
		SampleAspectRatio  string `json:"sample_aspect_ratio,omitempty"`
		DisplayAspectRatio string `json:"display_aspect_ratio,omitempty"`
		PixFmt             string `json:"pix_fmt,omitempty"`
		Level              int    `json:"level,omitempty"`
		ColorRange         string `json:"color_range,omitempty"`
		ColorSpace         string `json:"color_space,omitempty"`
		ColorTransfer      string `json:"color_transfer,omitempty"`
		ColorPrimaries     string `json:"color_primaries,omitempty"`
		ChromaLocation     string `json:"chroma_location,omitempty"`
		FieldOrder         string `json:"field_order,omitempty"`
		Refs               int    `json:"refs,omitempty"`
		IsAvc              string `json:"is_avc,omitempty"`
		NalLengthSize      string `json:"nal_length_size,omitempty"`
		ID                 string `json:"id"`
		RFrameRate         string `json:"r_frame_rate"`
		AvgFrameRate       string `json:"avg_frame_rate"`
		TimeBase           string `json:"time_base"`
		StartPts           int    `json:"start_pts"`
		StartTime          string `json:"start_time"`
		DurationTs         int    `json:"duration_ts"`
		Duration           string `json:"duration"`
		BitRate            string `json:"bit_rate,omitempty"`
		BitsPerRawSample   string `json:"bits_per_raw_sample,omitempty"`
		NbFrames           string `json:"nb_frames"`
		ExtradataSize      int    `json:"extradata_size"`
		Disposition        struct {
			Default         int `json:"default"`
			Dub             int `json:"dub"`
			Original        int `json:"original"`
			Comment         int `json:"comment"`
			Lyrics          int `json:"lyrics"`
			Karaoke         int `json:"karaoke"`
			Forced          int `json:"forced"`
			HearingImpaired int `json:"hearing_impaired"`
			VisualImpaired  int `json:"visual_impaired"`
			CleanEffects    int `json:"clean_effects"`
			AttachedPic     int `json:"attached_pic"`
			TimedThumbnails int `json:"timed_thumbnails"`
			NonDiegetic     int `json:"non_diegetic"`
			Captions        int `json:"captions"`
			Descriptions    int `json:"descriptions"`
			Metadata        int `json:"metadata"`
			Dependent       int `json:"dependent"`
			StillImage      int `json:"still_image"`
		} `json:"disposition"`
		Tags struct {
			Language    string `json:"language"`
			HandlerName string `json:"handler_name"`
			VendorID    string `json:"vendor_id"`
			Encoder     string `json:"encoder"`
			Timecode    string `json:"timecode"`
		} `json:"tags,omitempty"`
		SampleFmt      string `json:"sample_fmt,omitempty"`
		SampleRate     string `json:"sample_rate,omitempty"`
		Channels       int    `json:"channels,omitempty"`
		ChannelLayout  string `json:"channel_layout,omitempty"`
		BitsPerSample  int    `json:"bits_per_sample,omitempty"`
		InitialPadding int    `json:"initial_padding,omitempty"`
		Tags0          struct {
			Language    string `json:"language"`
			HandlerName string `json:"handler_name"`
			VendorID    string `json:"vendor_id"`
		} `json:"tags,omitempty"`
		Tags1 struct {
			Language    string `json:"language"`
			HandlerName string `json:"handler_name"`
			Timecode    string `json:"timecode"`
		} `json:"tags,omitempty"`
	} `json:"streams"`
}