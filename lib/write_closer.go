package lib

import (
	"bytes"
	"io"
)

type WriteCloser struct {
	io.WriteCloser
	Buffer *bytes.Buffer
}

func NewWriterCloser(wr io.WriteCloser) WriteCloser {
	return WriteCloser{
		WriteCloser: wr,
		Buffer:      &bytes.Buffer{},
	}
}

func (w WriteCloser) Write(p []byte) (n int, err error) {
	n, err = w.WriteCloser.Write(p)
	if err != nil {
		return n, err
	}

	_, _ = w.Buffer.Write(p)
	return n, err
}
