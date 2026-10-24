package settlement

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"time"
)

// HandleLine is invoked with one JSONL line, without the trailing newline.
type HandleLine func(ctx context.Context, line []byte) error

// Process reads a JSONL stream. Blank lines are ignored. The first error
// from handle stops the stream and is returned. Lines already handled stay handled.
func Process(ctx context.Context, r io.Reader, handle HandleLine) (int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		owned := append([]byte(nil), line...)
		if err := handle(ctx, owned); err != nil {
			return n, err
		}
		n++
	}
	if err := sc.Err(); err != nil {
		return n, err
	}
	return n, nil
}

// Follow tails a JSONL file. Existing lines are applied first. New lines are
// applied as they are appended. Settlement apply is idempotent, so a restart
// that re-reads the file is safe. Missing files are waited on until ctx ends.
func Follow(ctx context.Context, path string, handle HandleLine) error {
	var offset int64
	partial := make([]byte, 0)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if !sleep(ctx, 300*time.Millisecond) {
					return ctx.Err()
				}
				continue
			}
			return err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		if info.Size() < offset {
			offset = 0
			partial = partial[:0]
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return err
		}
		buf := make([]byte, 32*1024)
		for {
			n, rerr := f.Read(buf)
			if n > 0 {
				offset += int64(n)
				partial = append(partial, buf[:n]...)
				for {
					i := bytes.IndexByte(partial, '\n')
					if i < 0 {
						break
					}
					line := bytes.TrimSpace(partial[:i])
					partial = append([]byte(nil), partial[i+1:]...)
					if len(line) == 0 {
						continue
					}
					if err := handle(ctx, line); err != nil {
						f.Close()
						return err
					}
				}
			}
			if errors.Is(rerr, io.EOF) {
				break
			}
			if rerr != nil {
				f.Close()
				return rerr
			}
		}
		f.Close()
		if !sleep(ctx, 300*time.Millisecond) {
			return ctx.Err()
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
