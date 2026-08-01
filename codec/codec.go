package codec

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"time"

	"jonbaldie/gleam/cache"
)

// Codec defines an interface for serialization of cache items.
type Codec interface {
	Encode(item cache.CacheItem) ([]byte, error)
	Decode(data []byte) (*cache.CacheItem, error)
}

// BinaryCodec implements the Codec interface using a binary format.
type BinaryCodec struct{}

// Encode serializes a CacheItem to a byte slice.
func (c *BinaryCodec) Encode(item cache.CacheItem) ([]byte, error) {
	return encodeCacheItem(item)
}

// Decode deserializes a byte slice back into a CacheItem.
func (c *BinaryCodec) Decode(data []byte) (*cache.CacheItem, error) {
	return decodeCacheItem(data)
}

func encodeCacheItem(item cache.CacheItem) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeTo(&buf, item); err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	return []byte(encoded), nil
}

func writeU32(w io.Writer, n uint32) error {
	return binary.Write(w, binary.LittleEndian, n)
}

// writeSized writes a uint32 length prefix followed by b.
func writeSized(w io.Writer, b []byte) error {
	if err := writeU32(w, uint32(len(b))); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

func writeHeaderEntry(w io.Writer, key string, values []string) error {
	if err := writeSized(w, []byte(key)); err != nil {
		return err
	}
	if err := writeU32(w, uint32(len(values))); err != nil {
		return err
	}
	for _, value := range values {
		if err := writeSized(w, []byte(value)); err != nil {
			return err
		}
	}
	return nil
}

func encodeHeaders(w io.Writer, header http.Header) error {
	if err := writeU32(w, uint32(len(header))); err != nil {
		return err
	}
	for key, values := range header {
		if err := writeHeaderEntry(w, key, values); err != nil {
			return err
		}
	}
	return nil
}

func encodeExpiration(w io.Writer, expiration time.Time) error {
	expirationBytes, err := expiration.MarshalBinary()
	if err != nil {
		return err
	}
	return writeSized(w, expirationBytes)
}

func validateStatus(status uint32) error {
	if status < 100 || status > 999 {
		return fmt.Errorf("cache item: invalid status code %d", status)
	}
	return nil
}

func encodeTo(w io.Writer, item cache.CacheItem) error {
	if err := writeSized(w, item.Content); err != nil {
		return err
	}

	status := uint32(item.Status)
	if err := validateStatus(status); err != nil {
		return err
	}
	if err := writeU32(w, status); err != nil {
		return err
	}
	if err := encodeHeaders(w, item.Header); err != nil {
		return err
	}
	return encodeExpiration(w, item.Expiration)
}

// readCount reads a uint32 length/count prefix and rejects any value that
// cannot be backed by the bytes remaining in r. Cache entries come from Redis
// and may be corrupt or maliciously crafted; without this bound a prefix of
// 0xFFFFFFFF would drive a multi-gigabyte allocation from a few input bytes.
func readCount(r *bytes.Reader) (uint32, error) {
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return 0, err
	}
	if int64(n) > int64(r.Len()) {
		return 0, fmt.Errorf("cache item: prefix %d exceeds %d remaining bytes", n, r.Len())
	}
	return n, nil
}

// readSized reads a uint32 length prefix then exactly that many bytes from r.
func readSized(r *bytes.Reader) ([]byte, error) {
	n, err := readCount(r)
	if err != nil {
		return nil, err
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}

func decodeStatus(r *bytes.Reader) (int, error) {
	var status uint32
	if err := binary.Read(r, binary.LittleEndian, &status); err != nil {
		return 0, err
	}
	if err := validateStatus(status); err != nil {
		return 0, err
	}
	return int(status), nil
}

func decodeHeaderValues(r *bytes.Reader) ([]string, error) {
	valuesLen, err := readCount(r)
	if err != nil {
		return nil, err
	}
	values := make([]string, valuesLen)
	for j := uint32(0); j < valuesLen; j++ {
		value, err := readSized(r)
		if err != nil {
			return nil, err
		}
		values[j] = string(value)
	}
	return values, nil
}

func decodeHeaders(r *bytes.Reader) (http.Header, error) {
	headerLen, err := readCount(r)
	if err != nil {
		return nil, err
	}
	header := make(http.Header, headerLen)
	for i := uint32(0); i < headerLen; i++ {
		key, err := readSized(r)
		if err != nil {
			return nil, err
		}
		values, err := decodeHeaderValues(r)
		if err != nil {
			return nil, err
		}
		header[string(key)] = values
	}
	return header, nil
}

func decodeExpiration(r *bytes.Reader) (time.Time, error) {
	expirationBytes, err := readSized(r)
	if err != nil {
		return time.Time{}, err
	}
	var expiration time.Time
	if err := expiration.UnmarshalBinary(expirationBytes); err != nil {
		return time.Time{}, err
	}
	return expiration, nil
}

func decodeCacheItem(data []byte) (*cache.CacheItem, error) {
	decoded, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, err
	}

	buf := bytes.NewReader(decoded)
	item := &cache.CacheItem{}

	if item.Content, err = readSized(buf); err != nil {
		return nil, err
	}
	if item.Status, err = decodeStatus(buf); err != nil {
		return nil, err
	}
	if item.Header, err = decodeHeaders(buf); err != nil {
		return nil, err
	}
	if item.Expiration, err = decodeExpiration(buf); err != nil {
		return nil, err
	}

	return item, nil
}
