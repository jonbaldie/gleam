package codec

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
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
}

func TestDecode_InvalidStatus(t *testing.T) {
	codec := &BinaryCodec{}

	var buf bytes.Buffer
	// Write content len and content
	binary.Write(&buf, binary.LittleEndian, uint32(4))
	buf.Write([]byte("test"))

	// Write invalid status
	binary.Write(&buf, binary.LittleEndian, uint32(99))

	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	if _, err := codec.Decode([]byte(encoded)); err == nil || !strings.Contains(err.Error(), "invalid status code") {
		t.Errorf("Expected invalid status code error, got %v", err)
	}

	buf.Reset()
	binary.Write(&buf, binary.LittleEndian, uint32(4))
	buf.Write([]byte("test"))
	binary.Write(&buf, binary.LittleEndian, uint32(1000))
	encoded = base64.StdEncoding.EncodeToString(buf.Bytes())
	if _, err := codec.Decode([]byte(encoded)); err == nil || !strings.Contains(err.Error(), "invalid status code") {
		t.Errorf("Expected invalid status code error, got %v", err)
	}
}

func TestDecode_Truncated(t *testing.T) {
	codec := &BinaryCodec{}

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(100))
	// don't write 100 bytes, just 4
	buf.Write([]byte("test"))

	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	if _, err := codec.Decode([]byte(encoded)); err == nil {
		t.Errorf("Expected error for truncated payload")
	}
}

func TestDecode_InvalidExpiration(t *testing.T) {
	codec := &BinaryCodec{}

	// Encode a valid item
	item := cache.CacheItem{
		Content: []byte("test"),
		Header:  http.Header{},
		Status:  200,
		Expiration: time.Now(),
	}
	encoded, _ := codec.Encode(item)
	decoded, _ := base64.StdEncoding.DecodeString(string(encoded))

	// Truncate expiration
	decoded = decoded[:len(decoded)-2] 
	encodedBad := base64.StdEncoding.EncodeToString(decoded)

	if _, err := codec.Decode([]byte(encodedBad)); err == nil {
		t.Errorf("Expected error for invalid expiration")
	}
}

func TestReadCount_Exceeds(t *testing.T) {
	reader := bytes.NewReader([]byte{0xff, 0xff, 0xff, 0xff, 0x00})
	if _, err := readCount(reader); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("Expected exceeds error, got %v", err)
	}
}
