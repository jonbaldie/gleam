package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

const redisFormatMagic = "\x00gleam1\n"
const redisFormatHeaderSize = 84
const redisMaxMetadataSize = 16 << 20

func encodeRedisItem(item CacheItem) ([]byte, error) {
	metadata := item
	metadata.Content = nil
	encoded, err := encodeCacheItem(metadata)
	if err != nil {
		return nil, err
	}
	if len(encoded) > redisMaxMetadataSize {
		return encodeCacheItem(item)
	}
	out := make([]byte, redisFormatHeaderSize+len(encoded)+len(item.Content))
	copy(out, redisFormatMagic)
	binary.BigEndian.PutUint32(out[8:12], uint32(len(encoded)))
	binary.BigEndian.PutUint64(out[12:20], uint64(len(item.Content)))
	bodyDigest := sha256.Sum256(item.Content)
	copy(out[52:redisFormatHeaderSize], bodyDigest[:])
	copy(out[redisFormatHeaderSize:], encoded)
	copy(out[redisFormatHeaderSize+len(encoded):], item.Content)
	digest := sha256.New()
	_, _ = digest.Write(out[:20])
	_, _ = digest.Write(out[52 : redisFormatHeaderSize+len(encoded)])
	copy(out[20:52], digest.Sum(nil))
	return out, nil
}

func decodeRedisItem(data []byte) (*CacheItem, error) {
	if !bytes.HasPrefix(data, []byte(redisFormatMagic)) {
		return decodeCacheItem(data)
	}
	if len(data) < redisFormatHeaderSize {
		return nil, fmt.Errorf("truncated Redis cache header")
	}
	metadataLen := uint64(binary.BigEndian.Uint32(data[8:12]))
	bodyLen := binary.BigEndian.Uint64(data[12:20])
	if metadataLen > redisMaxMetadataSize || metadataLen > uint64(len(data)-redisFormatHeaderSize) || bodyLen != uint64(len(data)-redisFormatHeaderSize)-metadataLen {
		return nil, fmt.Errorf("invalid Redis cache lengths")
	}
	end := redisFormatHeaderSize + int(metadataLen)
	item, err := decodeRedisMetadata(data[:end])
	if err != nil {
		return nil, fmt.Errorf("invalid Redis cache metadata")
	}
	bodyDigest := sha256.Sum256(data[end:])
	if !bytes.Equal(data[52:redisFormatHeaderSize], bodyDigest[:]) {
		return nil, fmt.Errorf("corrupt Redis cache body")
	}
	item.Content = make([]byte, int(bodyLen))
	copy(item.Content, data[end:])
	return item, nil
}

func decodeRedisMetadata(data []byte) (*CacheItem, error) {
	if len(data) < redisFormatHeaderSize || !bytes.HasPrefix(data, []byte(redisFormatMagic)) || uint64(binary.BigEndian.Uint32(data[8:12])) != uint64(len(data)-redisFormatHeaderSize) {
		return nil, fmt.Errorf("invalid Redis cache metadata header")
	}
	digest := sha256.New()
	_, _ = digest.Write(data[:20])
	_, _ = digest.Write(data[52:])
	if !bytes.Equal(data[20:52], digest.Sum(nil)) {
		return nil, fmt.Errorf("corrupt Redis cache metadata")
	}
	item, err := decodeCacheItem(data[redisFormatHeaderSize:])
	if err != nil || len(item.Content) != 0 {
		return nil, fmt.Errorf("invalid Redis cache metadata")
	}
	return item, nil
}
