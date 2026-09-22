package protocol

import (
	"bytes"
	"encoding/json"
)

// Protocol and application limits are deliberately kept next to the wire
// types.  The Native Messaging frame limit is enforced by the host package;
// the job limits are enforced before a job can reach a queue implementation.
const (
	ProtocolVersion      = 1
	MaxFrameBytes        = 1 << 20 // 1 MiB, excluding the four-byte prefix.
	MaxSerializedJobByte = 972800  // 950 KiB.
	MaxTranscriptBytes   = 460800  // 450 KiB per transcript representation.
	MaxSourceURLBytes    = 2048
)

// Transcript job kinds. A discussion and a lecture may share the same numeric
// identity, so the kind disambiguates queue identity and repository paths.
const (
	KindLecture    = "lecture"
	KindDiscussion = "discussion"
)

// TranscriptJob is the only protocol value allowed to carry transcript text.
// Keep the field order in the canonical schema order: encoding/json preserves
// struct field order, which is used for size measurements and cross-language
// fixtures.
type TranscriptJob struct {
	SchemaVersion         int    `json:"schemaVersion"`
	Kind                  string `json:"kind"`
	LectureKey            string `json:"lectureKey"`
	CourseSlug            string `json:"courseSlug"`
	CourseName            string `json:"courseName"`
	Term                  string `json:"term"`
	LectureNumber         int    `json:"lectureNumber"`
	LectureDate           string `json:"lectureDate"`
	SourceURL             string `json:"sourceUrl"`
	CapturedAt            string `json:"capturedAt"`
	Transcript            string `json:"transcript"`
	TimestampedTranscript string `json:"timestampedTranscript"`
	ContentHash           string `json:"contentHash"`
}

// MarshalCanonical returns compact UTF-8 JSON with no trailing newline.  It
// intentionally does not sort keys: the schema's field order is canonical for
// TranscriptJob byte measurements.  HTML escaping is disabled so the bytes
// match the TypeScript canonical serializer (JSON.stringify), which is the
// cross-language size report and fixture authority.
func (j TranscriptJob) MarshalCanonical() ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(j); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}
