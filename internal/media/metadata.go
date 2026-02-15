package media

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

type ExtractedMetadata struct {
	CaptureTime string
	GPSLat      sql.NullFloat64
	GPSLon      sql.NullFloat64
	Make        sql.NullString
	Model       sql.NullString
	CameraYaw   sql.NullFloat64
	CameraPitch sql.NullFloat64
	CameraRoll  sql.NullFloat64
	RawJSON     string
}

var (
	regexYaw   = regexp.MustCompile(`(?i)GimbalYawDegree\s*=\s*"([\-0-9.]+)"`)
	regexPitch = regexp.MustCompile(`(?i)GimbalPitchDegree\s*=\s*"([\-0-9.]+)"`)
	regexRoll  = regexp.MustCompile(`(?i)GimbalRollDegree\s*=\s*"([\-0-9.]+)"`)
)

func ExtractMetadata(filePath, kind string) (ExtractedMetadata, error) {
	meta := ExtractedMetadata{CaptureTime: ""}
	raw := map[string]any{}

	if kind == "image" {
		imageMeta, err := parseImageEXIF(filePath)
		if err == nil {
			meta = imageMeta
		} else if !isNoExifError(err) {
			raw["exif_error"] = err.Error()
		}
	}

	if yaw, ok := parseDJIValue(filePath, regexYaw); ok {
		meta.CameraYaw = sql.NullFloat64{Float64: yaw, Valid: true}
		raw["dji_gimbal_yaw"] = yaw
	}
	if pitch, ok := parseDJIValue(filePath, regexPitch); ok {
		meta.CameraPitch = sql.NullFloat64{Float64: pitch, Valid: true}
		raw["dji_gimbal_pitch"] = pitch
	}
	if roll, ok := parseDJIValue(filePath, regexRoll); ok {
		meta.CameraRoll = sql.NullFloat64{Float64: roll, Valid: true}
		raw["dji_gimbal_roll"] = roll
	}

	if meta.CaptureTime == "" {
		if info, err := os.Stat(filePath); err == nil {
			meta.CaptureTime = info.ModTime().UTC().Format(time.RFC3339)
			raw["capture_time_fallback"] = "source_mod_time"
		}
	}

	if meta.CaptureTime == "" {
		meta.CaptureTime = time.Now().UTC().Format(time.RFC3339)
		raw["capture_time_fallback"] = "ingest_time"
	}

	b, err := json.Marshal(raw)
	if err != nil {
		return meta, err
	}
	meta.RawJSON = string(b)
	return meta, nil
}

func parseImageEXIF(filePath string) (ExtractedMetadata, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return ExtractedMetadata{}, err
	}
	defer f.Close()

	x, err := exif.Decode(f)
	if err != nil {
		return ExtractedMetadata{}, err
	}

	out := ExtractedMetadata{}
	if tm, err := x.DateTime(); err == nil {
		out.CaptureTime = tm.UTC().Format(time.RFC3339)
	}
	if lat, lon, err := x.LatLong(); err == nil {
		out.GPSLat = sql.NullFloat64{Float64: lat, Valid: true}
		out.GPSLon = sql.NullFloat64{Float64: lon, Valid: true}
	}
	if tag, err := x.Get(exif.Make); err == nil {
		if str, convErr := tag.StringVal(); convErr == nil {
			str = strings.TrimSpace(str)
			if str != "" {
				out.Make = sql.NullString{String: str, Valid: true}
			}
		}
	}
	if tag, err := x.Get(exif.Model); err == nil {
		if str, convErr := tag.StringVal(); convErr == nil {
			str = strings.TrimSpace(str)
			if str != "" {
				out.Model = sql.NullString{String: str, Valid: true}
			}
		}
	}
	return out, nil
}

func parseDJIValue(filePath string, rx *regexp.Regexp) (float64, bool) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	limitReader := io.LimitReader(f, 5*1024*1024)
	buf, err := io.ReadAll(limitReader)
	if err != nil {
		return 0, false
	}
	m := rx.FindSubmatch(buf)
	if len(m) != 2 {
		return 0, false
	}
	var val float64
	if _, err := fmt.Sscanf(string(m[1]), "%f", &val); err != nil {
		return 0, false
	}
	return val, true
}

func isNoExifError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "no exif") || strings.Contains(lower, "invalid jpeg format")
}
