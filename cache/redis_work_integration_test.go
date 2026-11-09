package cache

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRedisMetadataScriptOnlyReadsMetadataRanges(t *testing.T) {
	c, key := redisMetadataTestCache(t)
	opt := c.client.Options()
	if opt.TLSConfig != nil || opt.Username != "" || opt.Password != "" {
		t.Skip("MONITOR test requires an unauthenticated non-TLS test Redis")
	}
	item := CacheItem{Status: http.StatusOK, Header: http.Header{"Etag": {`"v1"`}}, Content: []byte(strings.Repeat("x", 1<<20))}
	c.Set(key, item, time.Minute)
	data, err := encodeRedisItem(item)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", opt.Addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(conn, "*1\r\n$7\r\nMONITOR\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	if line, err := reader.ReadString('\n'); err != nil || line != "+OK\r\n" {
		t.Fatalf("MONITOR response = %q, %v", line, err)
	}
	if _, ok := c.GetMetadata(key); !ok {
		t.Fatal("metadata lookup failed")
	}
	marker := key + "-end"
	if err := c.client.Echo(context.Background(), marker).Err(); err != nil {
		t.Fatal(err)
	}
	var commands []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, fmt.Sprintf("%q", marker)) {
			break
		}
		if strings.Contains(line, " lua]") && strings.Contains(line, fmt.Sprintf("%q", key)) {
			commands = append(commands, line)
		}
	}
	want := []string{
		fmt.Sprintf(`"GETRANGE" %q "0" "19"`, key),
		fmt.Sprintf(`"STRLEN" %q`, key),
		fmt.Sprintf(`"GETRANGE" %q "0" "%d"`, key, len(data)-len(item.Content)-1),
	}
	if len(commands) != len(want) {
		t.Fatalf("script commands = %q", commands)
	}
	for i := range want {
		if !strings.Contains(commands[i], want[i]) {
			t.Fatalf("script command %d = %q, want %q", i, commands[i], want[i])
		}
	}
}
