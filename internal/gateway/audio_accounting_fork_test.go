package gateway

import (
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

const scopedAudioDone = "data: {\"type\":\"speech.audio.done\",\"usage\":{\"input_tokens\":14,\"output_tokens\":7,\"total_tokens\":21}}\n\n"

func TestScopedAudioRequiresCompleteTokenEvidence(t *testing.T) {
	for _, tt := range []struct {
		name, body, contentType string
		speech, clipped, known  bool
		input, output           int
	}{
		{"json", `{"usage":{"type":"tokens","input_tokens":14,"output_tokens":7,"total_tokens":21}}`, "application/json", false, false, true, 14, 7},
		{"explicit-zero", `{"usage":{"input_tokens":0,"output_tokens":0}}`, "application/json", false, false, true, 0, 0},
		{"missing-output", `{"usage":{"input_tokens":14}}`, "application/json", false, false, false, 14, 0},
		{"missing-input", `{"usage":{"output_tokens":7}}`, "application/json", false, false, false, 0, 7},
		{"null-output", `{"usage":{"input_tokens":14,"output_tokens":null}}`, "application/json", false, false, false, 14, 0},
		{"negative", `{"usage":{"input_tokens":14,"output_tokens":-1}}`, "application/json", false, false, false, 0, 0},
		{"fraction", `{"usage":{"input_tokens":1.5,"output_tokens":0}}`, "application/json", false, false, false, 0, 0},
		{"string", `{"usage":{"input_tokens":"0","output_tokens":0}}`, "application/json", false, false, false, 0, 0},
		{"overflow", `{"usage":{"input_tokens":` + strconv.Itoa(int(^uint(0)>>1)) + `,"output_tokens":1}}`, "application/json", false, false, false, 0, 0},
		{"contradictory-total", `{"usage":{"input_tokens":14,"output_tokens":7,"total_tokens":14}}`, "application/json", false, false, false, 14, 7},
		{"duration-not-tokens", `{"usage":{"type":"duration","seconds":0,"input_tokens":0,"output_tokens":0}}`, "application/json", false, false, false, 0, 0},
		{"binary-not-json", `{"usage":{"input_tokens":0,"output_tokens":0}}`, "audio/mpeg", true, false, false, 0, 0},
		{"text-not-json", `{"usage":{"input_tokens":0,"output_tokens":0}}`, "text/plain", false, false, false, 0, 0},
		{"clipped-json", `{"usage":{"input_tokens":0,"output_tokens":0}}`, "application/json", true, true, false, 0, 0},
		{"done", scopedAudioDone, "text/event-stream; charset=utf-8", true, false, true, 14, 7},
		{"crlf", strings.ReplaceAll(scopedAudioDone, "\n", "\r\n"), "text/event-stream", true, false, true, 14, 7},
		{"comments-after-done", scopedAudioDone + ": keepalive\n\n", "text/event-stream", true, false, true, 14, 7},
		{"truncated-event", strings.TrimSuffix(scopedAudioDone, "\n"), "text/event-stream", true, false, false, 0, 0},
		{"partial-delta", strings.ReplaceAll(scopedAudioDone, "speech.audio.done", "speech.audio.delta"), "text/event-stream", true, false, false, 0, 0},
		{"error-after-done", scopedAudioDone + "data: {\"type\":\"error\"}\n\n", "text/event-stream", true, false, false, 0, 0},
		{"empty-error-event", scopedAudioDone + "event: error\n\n", "text/event-stream", true, false, false, 0, 0},
		{"unframed-clipped", scopedAudioDone, "text/event-stream", true, true, false, 0, 0},
		{"framed-after-clipping", "partial\n\n" + scopedAudioDone, "text/event-stream", true, true, true, 14, 7},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := audioBudgetReply([]byte(tt.body), tt.contentType, tt.speech, tt.clipped)
			if got.complete != tt.known || got.input != tt.input || got.output != tt.output {
				t.Fatalf("accounting=%+v, want known=%v input=%d output=%d", got, tt.known, tt.input, tt.output)
			}
		})
	}
}

type audioTailReadError struct{ io.Reader }

func (r audioTailReadError) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	if err == io.EOF {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

type audioTailWriteError struct{ *httptest.ResponseRecorder }

func (w audioTailWriteError) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestScopedAudioTailCopiesWithoutHoldingTheAnswer(t *testing.T) {
	body := strings.Repeat("data: {\"type\":\"speech.audio.delta\",\"audio\":\"AAAA\"}\n\n", 800) + scopedAudioDone
	w := httptest.NewRecorder()
	tail, clipped, err := copyAnswerTail(w, strings.NewReader(body))
	if err != nil || !clipped || len(tail) > speechTail || w.Body.String() != body || !w.Flushed {
		t.Fatalf("copy changed bytes/flush or exceeded tail bound: tail=%d clipped=%v err=%v", len(tail), clipped, err)
	}
	if proof := audioBudgetReply(tail, "text/event-stream", true, clipped); !proof.complete || proof.input != 14 || proof.output != 7 {
		t.Fatalf("terminal frame after large audio: %+v", proof)
	}
	for _, writeFails := range []bool{false, true} {
		var cut error
		if writeFails {
			tail, clipped, cut = copyAnswerTail(audioTailWriteError{httptest.NewRecorder()}, strings.NewReader(scopedAudioDone))
		} else {
			tail, clipped, cut = copyAnswerTail(httptest.NewRecorder(), audioTailReadError{strings.NewReader(scopedAudioDone)})
		}
		if cut == nil || clipped || !bytes.Equal(tail, []byte(scopedAudioDone)) {
			t.Fatalf("lost received evidence on cut: tail=%q clipped=%v err=%v", tail, clipped, cut)
		}
	}
	// The legacy wrapper still drops counters on any cut, as before this split.
	in, out, err := copyAnswer(httptest.NewRecorder(), audioTailReadError{strings.NewReader(scopedAudioDone)})
	if !errors.Is(err, io.ErrUnexpectedEOF) || in != 0 || out != 0 {
		t.Fatalf("legacy cut changed: %d %d %v", in, out, err)
	}
}
