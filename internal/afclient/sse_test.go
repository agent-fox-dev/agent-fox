package afclient

import (
	"io"
	"strings"
	"testing"
)

func TestSSEReader_FramesIDEventAndData(t *testing.T) {
	big := strings.Repeat("x", maxSSELine-len("data: "))
	stream := ": keepalive\n\n" +
		"retry: 3000\nid: 1\nevent: job_state\nfoo: bar\ndata: one\n\n" +
		"data: two\r\nid: 2\r\nevent: step\r\n\r\n" + // CRLF, and the data line first
		"data: three,\ndata: split\n\n" +
		"event: lonely\nid: 9\n\n" + // no data: no event, and nothing of it is kept
		"data:" + big + "\n\n" + // no space after the colon, and a line of the longest length
		"id: 7\ndata: never ended\n"
	want := []sseFrame{
		{id: "1", event: "job_state", data: "one"},
		{id: "2", event: "step", data: "two"},
		{data: "three,\nsplit"},
		{data: big},
	}
	r := newSSEReader(strings.NewReader(stream))
	for i, w := range want {
		got, err := r.next()
		if err != nil || got != w {
			t.Fatalf("frame %d = %.60q, %v, want %.60q", i+1, got, err, w)
		}
	}
	if _, err := r.next(); err != io.EOF {
		t.Errorf("after the frames: %v, want io.EOF and the frame that did not end dropped", err)
	}
}

func TestSSEReader_ALineOverTheLimitIsAnError(t *testing.T) {
	r := newSSEReader(strings.NewReader("data: " + strings.Repeat("x", maxSSELine) + "\n\n"))
	if _, err := r.next(); err == nil || err == io.EOF {
		t.Errorf("err = %v, want the error of a line too long", err)
	}
}
