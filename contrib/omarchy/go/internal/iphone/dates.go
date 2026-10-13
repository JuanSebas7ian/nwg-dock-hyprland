package iphone

import (
	"os"
	"os/exec"
	"strings"
	"time"
)

// The file dates the device reports are not always when a photo was taken: a restored iPhone or originals
// downloaded from iCloud Photos carry the day they arrived (here 2025-11-06/07 for photos from 2023). The
// capture date comes from the metadata: EXIF DateTimeOriginal for photos, the QuickTime creation date for videos.

// metaRunner reads metadata with exiv2 / ffprobe; tests replace it.
var metaRunner = func(name string, args ...string) ([]byte, error) { return exec.Command(name, args...).Output() }

var videoExt = map[string]bool{".mov": true, ".mp4": true, ".m4v": true, ".hevc": true}

// CaptureDate is when the photo or video was taken; ok=false means the metadata had none (screenshots,
// edited exports) and the file's own date was used.
func CaptureDate(path string) (time.Time, bool) {
	ext := strings.ToLower(path[strings.LastIndexByte(path, '.')+1:])
	if videoExt["."+ext] {
		if b, err := metaRunner("ffprobe", "-v", "quiet", "-show_entries", "format_tags=com.apple.quicktime.creationdate,creation_time",
			"-of", "default=nw=1:nk=0", path); err == nil {
			var apple, utc string
			for _, l := range strings.Split(string(b), "\n") {
				if v, ok := strings.CutPrefix(l, "TAG:com.apple.quicktime.creationdate="); ok {
					apple = strings.TrimSpace(v)
				} else if v, ok := strings.CutPrefix(l, "TAG:creation_time="); ok {
					utc = strings.TrimSpace(v)
				}
			}
			for _, v := range []string{apple, utc} {
				for _, layout := range []string{"2006-01-02T15:04:05-0700", time.RFC3339Nano, time.RFC3339} {
					if t, err := time.Parse(layout, v); err == nil && t.Year() > 2000 {
						return t, true
					}
				}
			}
		}
	} else if b, err := metaRunner("exiv2", "-q", "-K", "Exif.Photo.DateTimeOriginal", "-Pv", "pr", path); err == nil {
		v := strings.TrimSpace(string(b))
		if t, err := time.ParseInLocation("2006:01:02 15:04:05", v, time.Local); err == nil && t.Year() > 2000 {
			return t, true
		}
	}
	if st, err := os.Stat(path); err == nil {
		return st.ModTime(), false
	}
	return time.Time{}, false
}
