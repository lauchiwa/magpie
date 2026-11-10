package gateway

import (
	"bytes"
	"encoding/json"
	"mime"
)

type audioAccounting struct {
	input, output int
	complete      bool
}

// Audio needs both counters: embedding's input-only evidence is insufficient.
// Duration-only usage belongs to another accounting unit, never token zero.
func audioBudgetTokens(b []byte) audioAccounting {
	var a struct {
		Usage struct {
			Type   string `json:"type"`
			Input  *int   `json:"input_tokens"`
			Output *int   `json:"output_tokens"`
			Total  *int   `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(b, &a) != nil || a.Usage.Type != "" && a.Usage.Type != "tokens" {
		return audioAccounting{}
	}
	u := a.Usage
	for _, n := range []*int{u.Input, u.Output, u.Total} {
		if n != nil && *n < 0 {
			return audioAccounting{}
		}
	}
	var out audioAccounting
	if u.Input != nil {
		out.input = *u.Input
	}
	if u.Output != nil {
		out.output = *u.Output
	}
	if out.input > int(^uint(0)>>1)-out.output {
		return audioAccounting{}
	}
	out.complete = u.Input != nil && u.Output != nil
	if u.Total != nil && (out.input > *u.Total || out.output != *u.Total-out.input) {
		out.complete = false
	}
	return out
}

func audioBudgetReply(b []byte, contentType string, speech, clipped bool) audioAccounting {
	kind, _, err := mime.ParseMediaType(contentType)
	if err != nil || kind == "" {
		return audioAccounting{}
	}
	if kind == "application/json" && !clipped {
		return audioBudgetTokens(b)
	}
	if !speech || kind != "text/event-stream" {
		return audioAccounting{}
	}
	// Require the final complete SSE event, not a delta carrying partial
	// usage or an earlier done followed by a failure. A clipped first frame
	// is never proof; later frames still have their complete boundaries.
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	if !bytes.HasSuffix(b, []byte("\n\n")) {
		return audioAccounting{}
	}
	frames := bytes.Split(b, []byte("\n\n"))
	for i := len(frames) - 1; i >= 0; i-- {
		var data []byte
		onlyComments := true
		for line := range bytes.SplitSeq(frames[i], []byte("\n")) {
			if len(line) > 0 && line[0] != ':' {
				onlyComments = false
			}
			if value, ok := bytes.CutPrefix(line, []byte("data:")); ok {
				value = bytes.TrimPrefix(value, []byte(" "))
				data = append(data, value...)
				data = append(data, '\n')
			}
		}
		if len(data) == 0 {
			if !onlyComments {
				return audioAccounting{}
			}
			continue
		}
		if clipped && i == 0 {
			return audioAccounting{}
		}
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &event) != nil || event.Type != "speech.audio.done" {
			return audioAccounting{}
		}
		return audioBudgetTokens(data)
	}
	return audioAccounting{}
}
