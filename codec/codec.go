package codec

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"

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

func encodeTo(w io.Writer, item cache.CacheItem) error {
	// Write content length and content
	contentLen := uint32(len(item.Content))
	if err := binary.Write(w, binary.LittleEndian, contentLen); err != nil {
		return err
	}
	if _, err := w.Write(item.Content); err != nil {
		return err
	}

	status := uint32(item.Status)
	if status < 100 || status > 999 {
		return fmt.Errorf("cache item: invalid status code %d", status)
	}
	if err := binary.Write(w, binary.LittleEndian, status); err != nil {
		return err
	}

	// Write the headers
	headerLen := uint32(len(item.Header))
	if err := binary.Write(w, binary.LittleEndian, headerLen); err != nil {
		return err
	}
	for key, values := range item.Header {
		// Write the header key
		keyLen := uint32(len(key))
		if err := binary.Write(w, binary.LittleEndian, keyLen); err != nil {
			return err
		}
		if _, err := w.Write([]byte(key)); err != nil {
			return err
		}

		// Write the number of values for this header key
		valuesLen := uint32(len(values))
		if err := binary.Write(w, binary.LittleEndian, valuesLen); err != nil {
			return err
		}
		for _, value := range values {
			// Write the value
			valueLen := uint32(len(value))
			if err := binary.Write(w, binary.LittleEndian, valueLen); err != nil {
				return err
			}
			if _, err := w.Write([]byte(value)); err != nil {
				return err
			}
		}
	}

	// Write expiration time
	expirationBytes, err := item.Expiration.MarshalBinary()
	if err != nil {
		return err
	}
	expirationLen := uint32(len(expirationBytes))
	if err := binary.Write(w, binary.LittleEndian, expirationLen); err != nil {
		return err
	}
	if _, err := w.Write(expirationBytes); err != nil {
		return err
	}
	return nil
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
// It collapses the repeated binary.Read/buf.Read pairs in decodeCacheItem,
// keeping cyclomatic complexity under the gocyclo threshold of 15.
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

	var status uint32
	if err := binary.Read(buf, binary.LittleEndian, &status); err != nil {
		return nil, err
	}
	if status < 100 || status > 999 {
		return nil, fmt.Errorf("cache item: invalid status code %d", status)
	}
	item.Status = int(status)

	headerLen, err := readCount(buf)
	if err != nil {
		return nil, err
	}
	item.Header = make(http.Header, headerLen)
	for i := uint32(0); i < headerLen; i++ {
		key, err := readSized(buf)
		if err != nil {
			return nil, err
		}

		valuesLen, err := readCount(buf)
		if err != nil {
			return nil, err
		}
		values := make([]string, valuesLen)
		for j := uint32(0); j < valuesLen; j++ {
			value, err := readSized(buf)
			if err != nil {
				return nil, err
			}
			values[j] = string(value)
		}

		item.Header[string(key)] = values
	}

	expirationBytes, err := readSized(buf)
	if err != nil {
		return nil, err
	}
	if err := item.Expiration.UnmarshalBinary(expirationBytes); err != nil {
		return nil, err
	}

	return item, nil
}
