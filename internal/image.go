package internal

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"errors"
)

var (
	resRegexp            = regexp.MustCompile("[0-9]+x[0-9]+$")
	ErrInvalidResolution = errors.New("invalid resolution")
)

type Imgmeta struct {
	Original   string `json:"original"`
	IsOriginal bool   `json:"is_original"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

// Name generates an image name; for Original images, name remains the same;
// for new images, name is formatted as {originalFilename_1200x700.extension}
func (img Imgmeta) Name() string {
	if img.IsOriginal {
		return img.Original
	}

	ext := filepath.Ext(img.Original)
	filenameWithoutExt := strings.TrimSuffix(img.Original, ext)
	return fmt.Sprintf("%s_%dx%d%s", filenameWithoutExt, img.Width, img.Height, ext)
}

func NewImageFromRequest(filename string, resolution string) (img Imgmeta, err error) {
	img.Original = filename

	if resolution == "" {
		img.IsOriginal = true
		return img, nil
	}

	// Handle invalid size
	if !resRegexp.MatchString(resolution) {
		return img, ErrInvalidResolution
	}
	size := strings.Split(resolution, "x")

	img.Width, err = strconv.Atoi(size[0])
	if err != nil {
		return img, ErrInvalidResolution
	}

	img.Height, err = strconv.Atoi(size[1])
	if err != nil {
		return img, ErrInvalidResolution
	}

	return
}
