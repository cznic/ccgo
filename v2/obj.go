// Copyright 2017 The CCGO Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccgo

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"time"
)

const (
	objVersion = "1"
)

var (
	objMagic = []byte{0xc6, 0x1f, 0xd0, 0xb5, 0xc4, 0x39, 0xad, 0x56}
)

func objWrite(out io.Writer, goos, goarch, binaryVersion string, magic []byte, in io.Reader) error {
	w := gzip.NewWriter(out)
	w.Header.Comment = "ccgo object file"
	var buf bytes.Buffer
	buf.Write(magic)
	fmt.Fprintf(&buf, "%s|%s|%v", goos, goarch, binaryVersion)
	w.Header.Extra = buf.Bytes()
	w.Header.ModTime = time.Now()
	w.Header.OS = 255 // Unknown OS.
	if _, err := io.Copy(w, in); err != nil {
		return err
	}

	return w.Close()
}
