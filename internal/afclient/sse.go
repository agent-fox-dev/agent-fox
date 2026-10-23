package afclient

import (
	"bufio"
	"io"
	"strings"
)

// maxSSELine is the longest line of the event stream that is accepted, line break aside.
const maxSSELine = 1 << 20

// sseFrame is one event of the stream as it was framed: the fields id, event and data.
type sseFrame struct {
	id, event, data string
}

// sseReader reads the frames of an event stream.
type sseReader struct {
	lines *bufio.Scanner
}

func newSSEReader(r io.Reader) *sseReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxSSELine+2) // a line of maxSSELine bytes and its CRLF
	return &sseReader{lines: sc}
}

// next reads the next frame. A frame is the lines id:, event: and data: up to an empty line;
// the lines of data are joined with a line break, a comment (a line that begins with a colon)
// and any other field are ignored, and a frame with no data is no event. It returns io.EOF at the end
// of the stream, where a frame that the empty line has not ended is dropped, and any other
// error when the stream cannot be read, or has a line over the limit.
func (s *sseReader) next() (sseFrame, error) {
	var f sseFrame
	var data []string
	for s.lines.Scan() {
		line := s.lines.Text()
		if line == "" {
			if len(data) == 0 {
				f = sseFrame{}
				continue
			}
			f.data = strings.Join(data, "\n")
			return f, nil
		}
		if line[0] == ':' {
			continue
		}
		name, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch name {
		case "id":
			f.id = value
		case "event":
			f.event = value
		case "data":
			data = append(data, value)
		}
	}
	if err := s.lines.Err(); err != nil {
		return sseFrame{}, err
	}
	return sseFrame{}, io.EOF
}
