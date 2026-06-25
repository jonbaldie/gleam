package codec

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"jonbaldie/gleam/cache"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestEncode_InvalidStatus(t *testing.T) {
	codec := &BinaryCodec{}

	// Status < 100
	item := cache.CacheItem{Status: 99}
	if _, err := codec.Encode(item); err == nil || !strings.Contains(err.Error(), "invalid status code") {
		t.Errorf("Expected invalid status code error, got %v", err)
	}

	// Status > 999
	item = cache.CacheItem{Status: 1000}
	if _, err := codec.Encode(item); err == nil || !strings.Contains(err.Error(), "invalid status code") {
		t.Errorf("Expected invalid status code error, got %v", err)
	}

	// Status 100 and 999 are valid
	item = cache.CacheItem{Status: 100}
	if _, err := codec.Encode(item); err != nil {
		t.Errorf("Status 100 should be valid, got %v", err)
	}
	item = cache.CacheItem{Status: 999}
	if _, err := codec.Encode(item); err != nil {
		t.Errorf("Status 999 should be valid, got %v", err)
	}
}

type failingWriter struct {
	failAfter int
	written   int
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.written+len(p) > f.failAfter {
		return 0, fmt.Errorf("mock write error")
	}
	f.written += len(p)
	return len(p), nil
}

func TestEncodeTo_WriteErrors(t *testing.T) {
	item := cache.CacheItem{
		Content:    []byte("test"),
		Header:     http.Header{"X-Test": {"val1", "val2"}},
		Status:     200,
		Expiration: time.Now().Truncate(time.Second),
	}

	// Figure out total size
	var buf bytes.Buffer
	_ = encodeTo(&buf, item)
	total := buf.Len()

	for i := 0; i < total; i++ {
		w := &failingWriter{failAfter: i}
		err := encodeTo(w, item)
		if err == nil {
			t.Fatalf("Expected error when failing after %d bytes", i)
		}
	}
}

func TestDecode_InvalidStatus(t *testing.T) {
	codec := &BinaryCodec{}

	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint32(4))
	_, _ = buf.Write([]byte("test"))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(99))

	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	if _, err := codec.Decode([]byte(encoded)); err == nil || !strings.Contains(err.Error(), "invalid status code") {
		t.Errorf("Expected invalid status code error, got %v", err)
	}

	buf.Reset()
	_ = binary.Write(&buf, binary.LittleEndian, uint32(4))
	_, _ = buf.Write([]byte("test"))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(1000))
	encoded = base64.StdEncoding.EncodeToString(buf.Bytes())
	if _, err := codec.Decode([]byte(encoded)); err == nil || !strings.Contains(err.Error(), "invalid status code") {
		t.Errorf("Expected invalid status code error, got %v", err)
	}
}

func TestDecode_Truncated(t *testing.T) {
	codec := &BinaryCodec{}

	item := cache.CacheItem{
		Content:    []byte("test"),
		Header:     http.Header{"X-Test": {"val1"}},
		Status:     200,
		Expiration: time.Now(),
	}
	encoded, _ := codec.Encode(item)
	decoded, _ := base64.StdEncoding.DecodeString(string(encoded))

	for i := 1; i < len(decoded); i++ {
		trunc := base64.StdEncoding.EncodeToString(decoded[:i])
		_, err := codec.Decode([]byte(trunc))
		if err == nil {
			t.Fatalf("Expected error for truncated payload at length %d", i)
		}
	}
}

func TestReadCount_Exceeds(t *testing.T) {
	reader := bytes.NewReader([]byte{0xff, 0xff, 0xff, 0xff, 0x00})
	n, err := readCount(reader)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("Expected exceeds error, got %v", err)
	}
	if n != 0 {
		t.Errorf("Expected n=0 on error, got %v", n)
	}

	reader = bytes.NewReader([]byte{})
	n, err = readCount(reader)
	if err == nil {
		t.Errorf("Expected EOF error")
	}
	if n != 0 {
		t.Errorf("Expected n=0 on error, got %v", n)
	}
}
