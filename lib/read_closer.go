package lib

import (
	"bytes"
	"io"
)

type ReadCloser struct {
	io.ReadCloser
	Buffer *bytes.Buffer
}

func NewReaderCloser(rd io.ReadCloser) ReadCloser {
	return ReadCloser{
		ReadCloser: rd,
		Buffer:     &bytes.Buffer{},
	}
}

func (rd ReadCloser) Read(p []byte) (n int, err error) {
	n, err = rd.ReadCloser.Read(p)
	if err != nil {
		return n, err
	}

	_, _ = rd.Buffer.Write(p)
	return n, err
}
