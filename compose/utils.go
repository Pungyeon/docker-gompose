package compose

import (
	"bytes"
	"fmt"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/docker/pkg/term"
	"io"
	"os"
)

func writeProcessPS(buffer *bytes.Buffer, processes map[string]Process) {
	for name, proc := range processes {
		buffer.WriteString(fmt.Sprintf("%15s | %15s | %10s | %10s\n", limit(proc.ID, 10), name, proc.Driver, proc.Status))
	}
}

func limit(str string, length int) string {
	if len(str) < length {
		return str
	}
	return str[:length]
}

func shortened(str string) string {
	if len(str) < 30 {
		return str
	}
	return "..." + str[len(str)-27:]
}

type Event struct {
	Status         string `json:"status"`
	Error          string `json:"error"`
	Progress       string `json:"progress"`
	ProgressDetail struct {
		Current int `json:"current"`
		Total   int `json:"total"`
	} `json:"progressDetail"`
}

func readToStdOut(reader io.ReadCloser) {
	defer reader.Close()

	termFd, isTerm := term.GetFdInfo(os.Stderr)
	jsonmessage.DisplayJSONMessagesStream(reader, os.Stderr, termFd, isTerm, nil)
}