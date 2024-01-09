package main

import (
  "fmt"
  "io"
  "sync"
)

const (
  smallBufferSize = 64
  maxInt = int(^uint(0) >> 1)
  readSize = 32 * 1024
)

type MultiReadBuffer struct {
  buf []byte
  offset int
  mu sync.Mutex
}

func (b *MultiReadBuffer) Bytes() []byte { 
  b.mu.Lock()
	defer b.mu.Unlock()
  // no offset advancing!
  return b.buf 
}

func (b *MultiReadBuffer) String() string {
	if b == nil { // Special case, useful in debugging.
		return "<nil>"
	}
  b.mu.Lock()
	defer b.mu.Unlock()
  // no offset advancing!
	return string(b.buf)
}

func (b *MultiReadBuffer) Len() int { 
  b.mu.Lock()
	defer b.mu.Unlock()
  return len(b.buf) - b.offset 
}

func (b *MultiReadBuffer) Reset() {
  b.mu.Lock()
	defer b.mu.Unlock()
	b.offset = 0
}

func (b *MultiReadBuffer) Clear() {
  b.mu.Lock()
	defer b.mu.Unlock()
	b.offset = 0
  b.buf = b.buf[:0]
}

func (b *MultiReadBuffer) grow(n int) (int, error) {
  // assuming the caller has obtained the lock!
  if b.buf == nil { // new
    ll := 2 * n
    if ll < smallBufferSize {
      ll = smallBufferSize
    }
		b.buf = make([]byte, n, ll)
		return 0, nil
	}
  l, c := len(b.buf), cap(b.buf)
  if n <= c-l { // has room
		b.buf = b.buf[:l+n] // modify slice length
		return l, nil
	}
  ll := l + n + c // fill n plus extra c
  if ll > maxInt {
    if l+n > maxInt {
      return -1, fmt.Errorf("too large (current %d, new %d, max %d)", l, n, maxInt)
    }
    ll = maxInt
  }
  buf := make([]byte, l+n, ll)
  copy(buf, b.buf) // copy does not adjust slice length
  b.buf = buf
  return l, nil
}

func (b *MultiReadBuffer) Write(p []byte) (n int, err error) {
  b.mu.Lock()
	defer b.mu.Unlock()
	m, err := b.grow(len(p))
	if err != nil {
		return 0, err
	}
	return copy(b.buf[m:], p), nil
}

func (b *MultiReadBuffer) WriteString(s string) (n int, err error) {
  b.mu.Lock()
	defer b.mu.Unlock()
  m, err := b.grow(len(s))
	if err != nil {
		return 0, err
	}
	return copy(b.buf[m:], s), nil
}

func (b *MultiReadBuffer) WriteStringf(format string, args ...interface{}) (n int, err error) {
  s := fmt.Sprintf(format, args...)
  return b.WriteString(s)
}

func (b *MultiReadBuffer) Read(p []byte) (n int, err error) {
  b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.buf) - b.offset == 0 { // no more to read
		return 0, io.EOF
	}
	n = copy(p, b.buf[b.offset:])
	b.offset += n
	return n, nil
}

func (b *MultiReadBuffer) ReadString() string {
	if b == nil { // Special case, useful in debugging.
		return "<nil>"
	}
  b.mu.Lock()
	defer b.mu.Unlock()
  s := string(b.buf[b.offset:])
  b.offset += len(s)
	return s
}

func (b *MultiReadBuffer) ReadFrom(r io.Reader) (n int64, err error) {
  buf := make([]byte, readSize)
	for {
    m, e := r.Read(buf)
		if m < 0 {
			return n, fmt.Errorf("negative read length %d", m)
		}

    mm, ee := b.Write(buf[:m]) // this will lock
    if ee != nil {
      return n, fmt.Errorf("ReadFrom: failed to write to self: %w", ee)
    } else if mm != m {
      return n, fmt.Errorf("ReadFrom: failed to write to self with short write (%d bytes), expected %d bytes", mm, m)
    }
		n += int64(m)
		if e == io.EOF {
			return n, nil // e is EOF, so return nil explicitly
		} else if e != nil {
			return n, e
		}
	}
}

func (b *MultiReadBuffer) WriteTo(w io.Writer) (n int64, err error) {
  b.mu.Lock()
	defer b.mu.Unlock()
	if nBytes := len(b.buf) - b.offset; nBytes > 0 {
		m, e := w.Write(b.buf[b.offset:])
		if m > nBytes {
			return n, fmt.Errorf("WriteTo: invalid Write count %d, should be %d", m, nBytes)
		}
		b.offset += m
		n = int64(m)
		if e != nil {
			return n, e
		}
		// all bytes should have been written, by definition of
		// Write method in io.Writer
		if m != nBytes {
			return n, io.ErrShortWrite
		}
	}
	return n, nil
}
